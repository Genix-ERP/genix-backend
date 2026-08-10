package handler

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// writePlatformAudit records one row in the append-only platform_audit_log for a
// super-admin / platform-plane mutation (SEC-05, docs/admin-panel/audit.md).
//
// It is fire-and-forget: a logging failure must never break the actual admin
// action, so errors are logged and swallowed. The actor is read from the JWT
// claims already validated by RequireSystemAdmin.
//
//	action        e.g. "tenant.activate", "user.delete", "tender.company.verify"
//	targetType    "tenant" | "user" | "company" | "mobile_version" | "category"
//	targetID      uuid or platform string (may be "")
//	targetTenant  tenant the action touched (nil when N/A)
//	before/after  any JSON-serialisable snapshot (nil when N/A)
func (h *Handler) writePlatformAudit(c *gin.Context, action, targetType, targetID string, targetTenant *uuid.UUID, before, after interface{}) {
	var actorID *uuid.UUID
	var actorEmail string
	if claims, ok := middleware.GetClaims(c); ok && claims != nil {
		id := claims.UserID
		actorID = &id
		actorEmail = claims.Email
	}

	beforeJSON := toJSONOrNil(before)
	afterJSON := toJSONOrNil(after)

	_, err := h.db.Exec(`
		INSERT INTO platform_audit_log
		    (actor_user_id, actor_email, action, target_type, target_id,
		     target_tenant_id, before_values, after_values, ip_address, user_agent)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9, $10)
	`, actorID, actorEmail, action, targetType, targetID,
		targetTenant, beforeJSON, afterJSON, c.ClientIP(), c.GetHeader("User-Agent"))
	if err != nil {
		h.log.Error("failed to write platform_audit_log", "error", err, "action", action)
	}
}

// toJSONOrNil marshals v for a JSONB column. It returns an UNTYPED nil (→ SQL
// NULL) when there is nothing to store; returning a typed []byte(nil) would make
// lib/pq send an empty string, which Postgres rejects as invalid JSON.
func toJSONOrNil(v interface{}) interface{} {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// ListPlatformAuditLog returns the platform audit trail (Sozlamalar → Audit).
// GET /admin/audit-log  — RequireSystemAdmin (gated by the parent group).
func (h *Handler) ListPlatformAuditLog(c *gin.Context) {
	page := getIntParam(c, "page", 1)
	limit := getIntParam(c, "limit", 50)
	if limit > 200 {
		limit = 200
	}
	action := c.Query("action")
	pagination := entity.NewPagination(page, limit)

	where := "WHERE 1=1"
	args := []interface{}{}
	idx := 1
	if action != "" {
		where += " AND action = $" + itoa(idx)
		args = append(args, action)
		idx++
	}

	var total int
	h.db.QueryRow("SELECT COUNT(*) FROM platform_audit_log "+where, args...).Scan(&total)
	pagination.Calculate(total)

	query := `
		SELECT id, actor_user_id, actor_email, action, target_type, target_id,
		       target_tenant_id, before_values, after_values, ip_address, user_agent, created_at
		FROM platform_audit_log ` + where +
		" ORDER BY created_at DESC LIMIT $" + itoa(idx) + " OFFSET $" + itoa(idx+1)
	args = append(args, pagination.Limit, pagination.Offset())

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("failed to list platform_audit_log", "error", err)
		response.InternalServerError(c, "")
		return
	}
	defer rows.Close()

	type Row struct {
		ID             uuid.UUID       `json:"id"`
		ActorUserID    *string         `json:"actor_user_id,omitempty"`
		ActorEmail     string          `json:"actor_email"`
		Action         string          `json:"action"`
		TargetType     *string         `json:"target_type,omitempty"`
		TargetID       *string         `json:"target_id,omitempty"`
		TargetTenantID *string         `json:"target_tenant_id,omitempty"`
		BeforeValues   json.RawMessage `json:"before_values,omitempty"`
		AfterValues    json.RawMessage `json:"after_values,omitempty"`
		IPAddress      *string         `json:"ip_address,omitempty"`
		UserAgent      *string         `json:"user_agent,omitempty"`
		CreatedAt      time.Time       `json:"created_at"`
	}

	var out []Row
	for rows.Next() {
		var r Row
		var before, after []byte
		var actorID, targetType, targetID, targetTenant, ip, ua, actorEmail sql.NullString
		if err := rows.Scan(
			&r.ID, &actorID, &actorEmail, &r.Action, &targetType, &targetID,
			&targetTenant, &before, &after, &ip, &ua, &r.CreatedAt,
		); err != nil {
			continue
		}
		r.ActorEmail = actorEmail.String
		if actorID.Valid {
			r.ActorUserID = &actorID.String
		}
		if targetType.Valid {
			r.TargetType = &targetType.String
		}
		if targetID.Valid {
			r.TargetID = &targetID.String
		}
		if targetTenant.Valid {
			r.TargetTenantID = &targetTenant.String
		}
		if ip.Valid {
			r.IPAddress = &ip.String
		}
		if ua.Valid {
			r.UserAgent = &ua.String
		}
		if len(before) > 0 {
			r.BeforeValues = before
		}
		if len(after) > 0 {
			r.AfterValues = after
		}
		out = append(out, r)
	}

	response.SuccessWithMeta(c, out, pagination)
}
