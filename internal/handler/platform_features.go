package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/crypto"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ============================================================================
// ONBOARDING  — "Yangi kompaniya ochish" (Phase 4). Creates tenant + owner
// invite + default provisioning in one transaction. No more manual DB rows.
// POST /admin/tenants  (capability: company.create)
// ============================================================================
func (h *Handler) CreatePlatformTenant(c *gin.Context) {
	var in struct {
		TenantCode     string `json:"tenant_code" binding:"required,min=2,max=50"`
		TenantName     string `json:"tenant_name" binding:"required,min=2,max=255"`
		OwnerEmail     string `json:"owner_email" binding:"required,email"`
		OwnerFirstName string `json:"owner_first_name" binding:"required"`
		OwnerLastName  string `json:"owner_last_name"`
		OwnerPhone     string `json:"owner_phone"`
		PlanCode       string `json:"plan_code"`
		TrialDays      int    `json:"trial_days"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	if in.PlanCode == "" {
		in.PlanCode = "free"
	}

	// Resolve plan limits (fall back to sane defaults if the plan is unknown).
	var includedUsers, graceDays, planTrialDays int
	var maxUsers sql.NullInt64
	err := h.db.QueryRow(`
		SELECT included_users, COALESCE(max_users, 0), trial_days, grace_days
		FROM platform_plans WHERE code = $1 AND is_active = true
	`, in.PlanCode).Scan(&includedUsers, &maxUsers, &planTrialDays, &graceDays)
	if err != nil {
		includedUsers, planTrialDays, graceDays = 3, 7, 30
	}
	if in.TrialDays <= 0 {
		in.TrialDays = planTrialDays
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalServerError(c, "")
		return
	}
	defer tx.Rollback()

	// Uniqueness checks.
	var dummy uuid.UUID
	if tx.QueryRow(`SELECT id FROM users WHERE lower(email) = lower($1) AND deleted_at IS NULL`, in.OwnerEmail).Scan(&dummy) == nil {
		response.Conflict(c, "Email already registered")
		return
	}
	if tx.QueryRow(`SELECT id FROM tenants WHERE code = $1`, in.TenantCode).Scan(&dummy) == nil {
		response.Conflict(c, "Tenant code already exists")
		return
	}

	now := time.Now()
	trialEnds := now.AddDate(0, 0, in.TrialDays)
	clearAt := trialEnds.AddDate(0, 0, graceDays)
	tenantID := uuid.New()
	maxU := includedUsers
	if maxUsers.Valid && maxUsers.Int64 > 0 {
		maxU = int(maxUsers.Int64)
	}
	settings, _ := json.Marshal(map[string]interface{}{
		"locale": map[string]string{"language": "uz", "timezone": "Asia/Tashkent", "default_currency": "UZS"},
	})
	_, err = tx.Exec(`
		INSERT INTO tenants (id, code, name, settings, subscription_plan, subscription_status,
		                     trial_ends_at, account_clear_at, max_users, paid_users, is_active)
		VALUES ($1, $2, $3, $4, $5, 'trialing', $6, $7, $8, $9, true)
	`, tenantID, in.TenantCode, in.TenantName, settings, in.PlanCode, trialEnds, clearAt, maxU, includedUsers)
	if err != nil {
		h.log.Error("onboarding: create tenant failed", "error", err)
		response.InternalServerError(c, "Failed to create company")
		return
	}

	// Owner user with an invite token (no usable password until they accept).
	ownerID := uuid.New()
	inviteToken, _ := crypto.GenerateRandomString(32)
	placeholderHash, _ := crypto.HashPassword(uuid.NewString())
	inviteExpires := now.AddDate(0, 0, 14)
	_, err = tx.Exec(`
		INSERT INTO users (id, tenant_id, email, password_hash, first_name, last_name, phone, role,
		                   settings, is_active, is_verified, is_system_admin,
		                   invite_token, invite_token_expires, invited_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'owner', '{}', true, false, false, $8, $9, $10, $10, $10)
	`, ownerID, tenantID, in.OwnerEmail, placeholderHash, in.OwnerFirstName, in.OwnerLastName,
		nullStr(in.OwnerPhone), inviteToken, inviteExpires, now)
	if err != nil {
		h.log.Error("onboarding: create owner failed", "error", err)
		response.InternalServerError(c, "Failed to create owner")
		return
	}

	roleID := uuid.New()
	tx.Exec(`INSERT INTO roles (id, tenant_id, name, code, description, is_system)
	         VALUES ($1, $2, 'Owner', 'owner', 'Tenant owner with full access', true)`, roleID, tenantID)
	tx.Exec(`INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)`, ownerID, roleID)

	// Default warehouse + stock location + units (mirrors self-service register).
	whID := uuid.New()
	tx.Exec(`INSERT INTO warehouses (id, tenant_id, code, name, is_default, is_active, reception_steps, delivery_steps)
	         VALUES ($1, $2, 'WH-MAIN', 'Main Warehouse', true, true, 1, 1)`, whID, tenantID)
	tx.Exec(`INSERT INTO warehouse_locations (id, warehouse_id, code, name, type, is_active)
	         VALUES ($1, $2, 'STOCK', 'Stock', 'storage', true)`, uuid.New(), whID)
	h.seedDefaultUnitsOfMeasure(tx, tenantID)

	if err := tx.Commit(); err != nil {
		h.log.Error("onboarding: commit failed", "error", err)
		response.InternalServerError(c, "")
		return
	}

	h.writePlatformAudit(c, "tenant.create", "tenant", tenantID.String(), &tenantID, nil,
		map[string]interface{}{"code": in.TenantCode, "name": in.TenantName, "owner_email": in.OwnerEmail,
			"plan": in.PlanCode, "trial_days": in.TrialDays})

	response.Created(c, gin.H{
		"tenant_id":    tenantID,
		"owner_id":     ownerID,
		"status":       "trialing",
		"trial_ends_at": trialEnds,
		"invite_token": inviteToken,
		"invite_link":  "/accept-invite?token=" + inviteToken,
	})
}

// SetTenantStatus drives the lifecycle: trialing → active → suspended → blocked
// and back. PUT /admin/tenants/:id/status {status, reason}  (capability: company.block)
func (h *Handler) SetTenantStatus(c *gin.Context) {
	tenantID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid tenant id")
		return
	}
	var in struct {
		Status string `json:"status" binding:"required"`
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "status is required")
		return
	}
	// Map the requested lifecycle action to (subscription_status, is_active).
	var subStatus string
	var isActive bool
	switch in.Status {
	case "active", "activate":
		subStatus, isActive = "active", true
	case "trialing", "trial":
		subStatus, isActive = "trialing", true
	case "suspended", "suspend", "past_due":
		subStatus, isActive = "past_due", true
	case "blocked", "block", "cancelled":
		subStatus, isActive = "cancelled", false
	default:
		response.BadRequest(c, "Unknown status")
		return
	}
	var before struct {
		Status   string
		IsActive bool
	}
	h.db.QueryRow(`SELECT subscription_status, is_active FROM tenants WHERE id = $1`, tenantID).Scan(&before.Status, &before.IsActive)

	res, err := h.db.Exec(`UPDATE tenants SET subscription_status = $1, is_active = $2, updated_at = NOW() WHERE id = $3 AND deleted_at IS NULL`,
		subStatus, isActive, tenantID)
	if err != nil {
		response.InternalServerError(c, "")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "Tenant")
		return
	}
	h.writePlatformAudit(c, "tenant.status", "tenant", tenantID.String(), &tenantID,
		map[string]interface{}{"subscription_status": before.Status, "is_active": before.IsActive},
		map[string]interface{}{"subscription_status": subStatus, "is_active": isActive, "reason": in.Reason})
	response.Success(c, gin.H{"id": tenantID, "subscription_status": subStatus, "is_active": isActive})
}

// ============================================================================
// PLAN CATALOG  (Phase 4)
// ============================================================================
func (h *Handler) ListPlatformPlans(c *gin.Context) {
	rows, err := h.db.Query(`
		SELECT code, display_name, price_per_user_monthly, included_users, max_users,
		       ai_quota, features, trial_days, grace_days, is_active, sort_order
		FROM platform_plans ORDER BY sort_order ASC
	`)
	if err != nil {
		response.InternalServerError(c, "")
		return
	}
	defer rows.Close()
	type Plan struct {
		Code         string          `json:"code"`
		DisplayName  string          `json:"display_name"`
		PricePerUser int64           `json:"price_per_user_monthly"`
		IncludedUsers int            `json:"included_users"`
		MaxUsers     *int            `json:"max_users"`
		AIQuota      *int            `json:"ai_quota"`
		Features     json.RawMessage `json:"features"`
		TrialDays    int             `json:"trial_days"`
		GraceDays    int             `json:"grace_days"`
		IsActive     bool            `json:"is_active"`
		SortOrder    int             `json:"sort_order"`
	}
	var out []Plan
	for rows.Next() {
		var p Plan
		var maxU, ai sql.NullInt64
		var feats []byte
		if err := rows.Scan(&p.Code, &p.DisplayName, &p.PricePerUser, &p.IncludedUsers, &maxU, &ai,
			&feats, &p.TrialDays, &p.GraceDays, &p.IsActive, &p.SortOrder); err != nil {
			continue
		}
		if maxU.Valid {
			v := int(maxU.Int64)
			p.MaxUsers = &v
		}
		if ai.Valid {
			v := int(ai.Int64)
			p.AIQuota = &v
		}
		if len(feats) > 0 {
			p.Features = feats
		}
		out = append(out, p)
	}
	response.Success(c, out)
}

// UpsertPlatformPlan updates a plan's pricing/limits. PUT /admin/plans/:code (plans.edit).
func (h *Handler) UpsertPlatformPlan(c *gin.Context) {
	code := c.Param("code")
	var in struct {
		DisplayName  *string `json:"display_name"`
		PricePerUser *int64  `json:"price_per_user_monthly"`
		IncludedUsers *int   `json:"included_users"`
		MaxUsers     *int    `json:"max_users"`
		AIQuota      *int    `json:"ai_quota"`
		TrialDays    *int    `json:"trial_days"`
		GraceDays    *int    `json:"grace_days"`
		IsActive     *bool   `json:"is_active"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	res, err := h.db.Exec(`
		UPDATE platform_plans SET
			display_name           = COALESCE($2, display_name),
			price_per_user_monthly = COALESCE($3, price_per_user_monthly),
			included_users         = COALESCE($4, included_users),
			max_users              = COALESCE($5, max_users),
			ai_quota               = COALESCE($6, ai_quota),
			trial_days             = COALESCE($7, trial_days),
			grace_days             = COALESCE($8, grace_days),
			is_active              = COALESCE($9, is_active),
			updated_at             = NOW()
		WHERE code = $1
	`, code, in.DisplayName, in.PricePerUser, in.IncludedUsers, in.MaxUsers, in.AIQuota, in.TrialDays, in.GraceDays, in.IsActive)
	if err != nil {
		response.InternalServerError(c, "")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "Plan")
		return
	}
	h.writePlatformAudit(c, "plan.update", "plan", code, nil, nil,
		map[string]interface{}{"price_per_user_monthly": in.PricePerUser, "is_active": in.IsActive})
	response.Success(c, gin.H{"code": code, "updated": true})
}

// ============================================================================
// PLATFORM STATS / SaaS OVERVIEW  (Phase 4) — GET /admin/stats
// All numbers are platform-wide server aggregates (fixes the client-side,
// paginated, owner-only stat wiring bug, audit Data-1).
// ============================================================================
func (h *Handler) GetPlatformStats(c *gin.Context) {
	out := gin.H{}

	// Tenant status counts.
	var totalCompanies, active, trialing, pastDue, blocked int
	h.db.QueryRow(`SELECT COUNT(*) FROM tenants WHERE deleted_at IS NULL`).Scan(&totalCompanies)
	h.db.QueryRow(`SELECT COUNT(*) FROM tenants WHERE deleted_at IS NULL AND subscription_status = 'active'`).Scan(&active)
	h.db.QueryRow(`SELECT COUNT(*) FROM tenants WHERE deleted_at IS NULL AND subscription_status = 'trialing'`).Scan(&trialing)
	h.db.QueryRow(`SELECT COUNT(*) FROM tenants WHERE deleted_at IS NULL AND subscription_status IN ('past_due','expired','cancelled')`).Scan(&pastDue)
	h.db.QueryRow(`SELECT COUNT(*) FROM tenants WHERE deleted_at IS NULL AND is_active = false`).Scan(&blocked)

	// Real platform-wide user count (NOT the paginated owner list).
	var totalUsers int
	h.db.QueryRow(`SELECT COUNT(*) FROM users WHERE deleted_at IS NULL`).Scan(&totalUsers)

	// This month: new companies + churn (deactivated).
	var newThisMonth, churnThisMonth int
	h.db.QueryRow(`SELECT COUNT(*) FROM tenants WHERE deleted_at IS NULL AND date_trunc('month', created_at) = date_trunc('month', NOW())`).Scan(&newThisMonth)
	h.db.QueryRow(`SELECT COUNT(*) FROM tenants WHERE deleted_at IS NULL AND is_active = false AND date_trunc('month', updated_at) = date_trunc('month', NOW())`).Scan(&churnThisMonth)

	// MRR: sum over active tenants of paid_users * plan price. Falls back to the
	// config per-user price when a tenant's plan isn't in the catalog.
	var mrr int64
	h.db.QueryRow(`
		SELECT COALESCE(SUM(t.paid_users * COALESCE(p.price_per_user_monthly, $1)), 0)
		FROM tenants t
		LEFT JOIN platform_plans p ON p.code = t.subscription_plan
		WHERE t.deleted_at IS NULL AND t.subscription_status = 'active'
	`, h.config.Multicard.PricePerUserMonthly).Scan(&mrr)

	out["companies"] = gin.H{
		"total": totalCompanies, "active": active, "trialing": trialing,
		"past_due": pastDue, "blocked": blocked,
	}
	out["total_users"] = totalUsers
	out["new_companies_this_month"] = newThisMonth
	out["churn_this_month"] = churnThisMonth
	out["mrr"] = mrr
	if trialing+active > 0 {
		out["trial_conversion"] = float64(active) / float64(active+trialing)
	} else {
		out["trial_conversion"] = 0
	}

	// Expiring within 7 days (trial or subscription).
	out["expiring_soon"] = h.queryTenantBrief(`
		SELECT id, name, code, subscription_status, subscription_plan, trial_ends_at
		FROM tenants
		WHERE deleted_at IS NULL AND trial_ends_at IS NOT NULL
		  AND trial_ends_at BETWEEN NOW() AND NOW() + INTERVAL '7 days'
		ORDER BY trial_ends_at ASC LIMIT 25`)

	// Recent signups.
	out["recent_signups"] = h.queryTenantBrief(`
		SELECT id, name, code, subscription_status, subscription_plan, trial_ends_at
		FROM tenants WHERE deleted_at IS NULL
		ORDER BY created_at DESC LIMIT 10`)

	// Daily active tenants (30d) — distinct tenants with a user login that day.
	type Point struct {
		Day    string `json:"day"`
		Active int    `json:"active_tenants"`
	}
	var series []Point
	drows, err := h.db.Query(`
		SELECT to_char(d::date, 'YYYY-MM-DD'),
		       (SELECT COUNT(DISTINCT u.tenant_id) FROM users u
		        WHERE u.last_login_at::date = d::date AND u.deleted_at IS NULL)
		FROM generate_series(NOW()::date - INTERVAL '29 days', NOW()::date, INTERVAL '1 day') d
		ORDER BY d`)
	if err == nil {
		defer drows.Close()
		for drows.Next() {
			var p Point
			if err := drows.Scan(&p.Day, &p.Active); err == nil {
				series = append(series, p)
			}
		}
	}
	out["daily_active_tenants"] = series

	response.Success(c, out)
}

type tenantBrief struct {
	ID                 uuid.UUID  `json:"id"`
	Name               string     `json:"name"`
	Code               string     `json:"code"`
	SubscriptionStatus string     `json:"subscription_status"`
	SubscriptionPlan   string     `json:"subscription_plan"`
	TrialEndsAt        *time.Time `json:"trial_ends_at,omitempty"`
}

func (h *Handler) queryTenantBrief(query string) []tenantBrief {
	rows, err := h.db.Query(query)
	if err != nil {
		return []tenantBrief{}
	}
	defer rows.Close()
	out := []tenantBrief{}
	for rows.Next() {
		var t tenantBrief
		var status, plan sql.NullString
		var te sql.NullTime
		if err := rows.Scan(&t.ID, &t.Name, &t.Code, &status, &plan, &te); err != nil {
			continue
		}
		t.SubscriptionStatus = status.String
		t.SubscriptionPlan = plan.String
		if te.Valid {
			t.TrialEndsAt = &te.Time
		}
		out = append(out, t)
	}
	return out
}

// ============================================================================
// IMPERSONATION  — "mijoz sifatida kirish" (Phase 3). POST /admin/impersonate.
// ============================================================================
func (h *Handler) Impersonate(c *gin.Context) {
	claims, _ := middleware.GetClaims(c)
	role := middleware.EffectivePlatformRole(claims)
	canFull := middleware.HasCapability(role, middleware.CapImpersonate)
	canRead := middleware.HasCapability(role, middleware.CapImpersonateRead)
	if !canFull && !canRead {
		response.Forbidden(c, "You do not have impersonation capability")
		return
	}

	var in struct {
		TenantID string `json:"tenant_id" binding:"required"`
		Reason   string `json:"reason" binding:"required"`
		ReadOnly bool   `json:"read_only"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "tenant_id and reason are required")
		return
	}
	tenantID, err := uuid.Parse(in.TenantID)
	if err != nil {
		response.BadRequest(c, "Invalid tenant_id")
		return
	}
	// tex_podderjka is always forced read-only.
	readOnly := in.ReadOnly || !canFull

	// Impersonate the tenant's owner.
	var ownerID uuid.UUID
	var ownerEmail string
	err = h.db.QueryRow(`
		SELECT u.id, COALESCE(u.email, '')
		FROM users u
		JOIN user_roles ur ON ur.user_id = u.id
		JOIN roles r ON r.id = ur.role_id
		WHERE u.tenant_id = $1 AND r.code = 'owner' AND u.deleted_at IS NULL
		ORDER BY u.created_at ASC LIMIT 1
	`, tenantID).Scan(&ownerID, &ownerEmail)
	if err != nil {
		// Fall back to any active user in the tenant.
		if err = h.db.QueryRow(`SELECT id, COALESCE(email,'') FROM users WHERE tenant_id = $1 AND deleted_at IS NULL ORDER BY created_at ASC LIMIT 1`, tenantID).
			Scan(&ownerID, &ownerEmail); err != nil {
			response.NotFound(c, "No user to impersonate in this tenant")
			return
		}
	}

	actorID := uuid.Nil
	if claims != nil && claims.PlatformUserID != nil {
		actorID = *claims.PlatformUserID
	} else if claims != nil {
		actorID = claims.UserID
	}

	token, expiresAt, err := h.jwtManager.GenerateImpersonationToken(ownerID, tenantID, ownerEmail, actorID, readOnly, time.Hour)
	if err != nil {
		response.InternalServerError(c, "")
		return
	}

	var tenantName string
	h.db.QueryRow(`SELECT name FROM tenants WHERE id = $1`, tenantID).Scan(&tenantName)

	// Platform audit trail.
	h.writePlatformAudit(c, "impersonation.start", "tenant", tenantID.String(), &tenantID, nil,
		map[string]interface{}{"reason": in.Reason, "read_only": readOnly, "target_user": ownerID.String()})

	// Tenant-visible activity: "Genix support kirdi" in the tenant's own feed.
	newVals, _ := json.Marshal(map[string]interface{}{
		"by": "Genix support", "reason": in.Reason, "read_only": readOnly,
	})
	h.db.Exec(`
		INSERT INTO audit_logs (id, tenant_id, user_id, action, entity_type, entity_id, new_values, created_at)
		VALUES ($1, $2, $3, 'support_access', 'impersonation', $4, $5, NOW())
	`, uuid.New(), tenantID, actorID, ownerID, newVals)

	response.Success(c, gin.H{
		"access_token": token,
		"expires_at":   expiresAt,
		"read_only":    readOnly,
		"tenant":       gin.H{"id": tenantID, "name": tenantName},
		"banner":       fmt.Sprintf("PLATFORMA: %s sifatida kirilgan%s", tenantName, readOnlySuffix(readOnly)),
	})
}

func readOnlySuffix(ro bool) string {
	if ro {
		return " (faqat ko'rish)"
	}
	return ""
}
