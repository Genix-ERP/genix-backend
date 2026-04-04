package handler

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// GetSubscriptionStatus returns the current tenant's trial / subscription state.
// GET /subscription/status
func (h *Handler) GetSubscriptionStatus(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "Tenant not resolved")
		return
	}

	var (
		status         string
		plan           string
		trialEndsAt    sql.NullTime
		accountClearAt sql.NullTime
		isActive       bool
	)

	err := h.db.QueryRow(`
		SELECT subscription_status, subscription_plan, trial_ends_at, account_clear_at, is_active
		FROM tenants
		WHERE id = $1 AND deleted_at IS NULL
	`, tenantID).Scan(&status, &plan, &trialEndsAt, &accountClearAt, &isActive)
	if err != nil {
		response.NotFound(c, "Tenant not found")
		return
	}

	now := time.Now()

	// Auto-advance status if needed (idempotent)
	if status == "trialing" && trialEndsAt.Valid && now.After(trialEndsAt.Time) {
		h.db.Exec(`UPDATE tenants SET subscription_status = 'past_due' WHERE id = $1`, tenantID)
		status = "past_due"
	}
	if accountClearAt.Valid && now.After(accountClearAt.Time) && status != "active" {
		h.db.Exec(`UPDATE tenants SET subscription_status = 'expired', is_active = false WHERE id = $1`, tenantID)
		status = "expired"
		isActive = false
	}

	resp := gin.H{
		"status":    status,
		"plan":      plan,
		"is_active": isActive,
	}
	if trialEndsAt.Valid {
		resp["trial_ends_at"] = trialEndsAt.Time
	}
	if accountClearAt.Valid {
		resp["account_clear_at"] = accountClearAt.Time
	}

	if trialEndsAt.Valid {
		days := int(trialEndsAt.Time.Sub(now).Hours() / 24)
		if days < 0 {
			days = 0
		}
		resp["trial_days_remaining"] = days
	}

	if accountClearAt.Valid {
		days := int(accountClearAt.Time.Sub(now).Hours() / 24)
		if days < 0 {
			days = 0
		}
		resp["days_until_clear"] = days
	}

	response.Success(c, resp)
}

// ActivateSubscription marks the tenant as paid/active (called after payment is confirmed).
// POST /subscription/activate
// Body: { "plan": "starter" | "professional" | "enterprise" }
func (h *Handler) ActivateSubscription(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "Tenant not resolved")
		return
	}

	var input struct {
		Plan string `json:"plan"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.Plan == "" {
		input.Plan = "starter"
	}

	validPlans := map[string]bool{"starter": true, "professional": true, "enterprise": true}
	if !validPlans[input.Plan] {
		response.BadRequest(c, "Invalid plan")
		return
	}

	_, err := h.db.Exec(`
		UPDATE tenants
		SET subscription_status = 'active',
		    subscription_plan   = $2,
		    is_active           = true,
		    trial_ends_at       = NULL,
		    account_clear_at    = NULL,
		    updated_at          = NOW()
		WHERE id = $1
	`, tenantID, input.Plan)
	if err != nil {
		response.InternalServerError(c, "Failed to activate subscription")
		return
	}

	response.Success(c, gin.H{"message": "Subscription activated", "plan": input.Plan})
}

// CleanExpiredTenants is a system-admin endpoint (or cron trigger) that hard-deletes
// all data for tenants whose account_clear_at has passed and status = 'expired'.
// POST /admin/clean-expired-tenants
func (h *Handler) CleanExpiredTenants(c *gin.Context) {
	now := time.Now()

	// First mark any overdue tenants as expired
	h.db.Exec(`
		UPDATE tenants
		SET subscription_status = 'expired', is_active = false
		WHERE deleted_at IS NULL
		  AND subscription_status NOT IN ('active', 'expired')
		  AND account_clear_at IS NOT NULL
		  AND account_clear_at < $1
	`, now)

	// Soft-delete expired tenants (set deleted_at)
	result, err := h.db.Exec(`
		UPDATE tenants
		SET deleted_at = NOW()
		WHERE deleted_at IS NULL
		  AND subscription_status = 'expired'
		  AND account_clear_at IS NOT NULL
		  AND account_clear_at < $1
	`, now)
	if err != nil {
		response.InternalServerError(c, "Failed to clean expired tenants")
		return
	}

	rows, _ := result.RowsAffected()
	c.JSON(http.StatusOK, gin.H{
		"success":          true,
		"tenants_archived": rows,
	})
}
