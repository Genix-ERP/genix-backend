package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/genixerp/genix-backend/internal/infrastructure/payment"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// planAmount returns the plan price in UZS and tiyin.
func (h *Handler) planAmount(plan string) (uzs int64, tiyin int64, ok bool) {
	switch plan {
	case "starter":
		uzs = h.config.Multicard.StarterAmount
	case "professional":
		uzs = h.config.Multicard.ProfessionalAmount
	case "enterprise":
		uzs = h.config.Multicard.EnterpriseAmount
	default:
		return 0, 0, false
	}
	return uzs, uzs * 100, true
}

// GetPlans returns available plans with UZS prices.
// GET /subscription/plans
func (h *Handler) GetPlans(c *gin.Context) {
	type plan struct {
		Key    string `json:"key"`
		Name   string `json:"name"`
		AmountUZS int64 `json:"amount_uzs"`
	}
	response.Success(c, []plan{
		{Key: "starter", Name: "Starter", AmountUZS: h.config.Multicard.StarterAmount},
		{Key: "professional", Name: "Professional", AmountUZS: h.config.Multicard.ProfessionalAmount},
		{Key: "enterprise", Name: "Enterprise", AmountUZS: h.config.Multicard.EnterpriseAmount},
	})
}

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
		days := int(trialEndsAt.Time.Sub(now).Hours() / 24)
		if days < 0 {
			days = 0
		}
		resp["trial_days_remaining"] = days
	}
	if accountClearAt.Valid {
		resp["account_clear_at"] = accountClearAt.Time
		days := int(accountClearAt.Time.Sub(now).Hours() / 24)
		if days < 0 {
			days = 0
		}
		resp["days_until_clear"] = days
	}

	response.Success(c, resp)
}

// ActivateSubscription marks the tenant as paid/active (manual override or test).
// POST /subscription/activate
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

// CreateCheckout creates a Multicard invoice and returns the checkout URL.
// POST /subscription/checkout
// Body: { "plan": "starter" | "professional" | "enterprise" }
func (h *Handler) CreateCheckout(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "Tenant not resolved")
		return
	}

	var input struct {
		Plan string `json:"plan" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Plan is required")
		return
	}

	amountUZS, amountTiyin, ok := h.planAmount(input.Plan)
	if !ok {
		response.BadRequest(c, "Invalid plan")
		return
	}

	// Unique invoice ID for this payment attempt
	invoiceID := fmt.Sprintf("genix-%s-%s", tenantID[:8], uuid.New().String()[:8])

	frontendURL := h.config.App.FrontendURL
	backendURL := h.config.App.BaseURL

	inv, err := h.multicardClient.CreateInvoice(c.Request.Context(), payment.InvoiceRequest{
		Amount:       amountTiyin,
		InvoiceID:    invoiceID,
		ReturnURL:    frontendURL + "/payment-success?plan=" + input.Plan,
		ReturnErrURL: frontendURL + "/payment-error",
		CallbackURL:  backendURL + "/api/v1/webhooks/multicard",
	})
	if err != nil {
		h.log.Error("Multicard create invoice failed", "error", err)
		response.InternalServerError(c, "Failed to create payment")
		return
	}

	// Record payment attempt
	h.db.Exec(`
		INSERT INTO subscription_payments
			(tenant_id, plan, amount_uzs, amount_tiyin, invoice_id, multicard_uuid, checkout_url, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending')
	`, tenantID, input.Plan, amountUZS, amountTiyin, invoiceID, inv.UUID, inv.CheckoutURL)

	response.Success(c, gin.H{
		"checkout_url": inv.CheckoutURL,
		"invoice_id":   invoiceID,
		"uuid":         inv.UUID,
	})
}

// MulticardWebhook receives payment status callbacks from Multicard.
// POST /webhooks/multicard  (public — no auth)
func (h *Handler) MulticardWebhook(c *gin.Context) {
	var payload payment.WebhookPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false})
		return
	}

	// Verify signature
	if !h.multicardClient.VerifySign(payload) {
		h.log.Warn("Multicard webhook: invalid signature", "invoice_id", payload.InvoiceID)
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Invalid signature"})
		return
	}

	// Look up the payment record
	var tenantID, plan string
	err := h.db.QueryRow(`
		SELECT tenant_id, plan FROM subscription_payments WHERE invoice_id = $1
	`, payload.InvoiceID).Scan(&tenantID, &plan)
	if err != nil {
		// Unknown invoice — still return 200 to stop retries
		c.JSON(http.StatusOK, gin.H{"success": true})
		return
	}

	// Update payment record
	h.db.Exec(`
		UPDATE subscription_payments
		SET status       = $2,
		    billing_id   = $3,
		    card_pan     = $4,
		    ps           = $5,
		    card_token   = $6,
		    payment_time = $7,
		    updated_at   = NOW()
		WHERE invoice_id = $1
	`, payload.InvoiceID, payload.Status, payload.BillingID,
		payload.CardPan, payload.PS, payload.CardToken, payload.PaymentTime)

	// Only activate on final success
	if payload.Status == "success" {
		h.db.Exec(`
			UPDATE tenants
			SET subscription_status = 'active',
			    subscription_plan   = $2,
			    is_active           = true,
			    trial_ends_at       = NULL,
			    account_clear_at    = NULL,
			    updated_at          = NOW()
			WHERE id = $1
		`, tenantID, plan)

		h.log.Info("Subscription activated via Multicard",
			"tenant_id", tenantID, "plan", plan, "invoice", payload.InvoiceID)
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// CleanExpiredTenants soft-deletes tenants whose account_clear_at has passed.
// POST /admin/clean-expired-tenants
func (h *Handler) CleanExpiredTenants(c *gin.Context) {
	now := time.Now()

	h.db.Exec(`
		UPDATE tenants SET subscription_status = 'expired', is_active = false
		WHERE deleted_at IS NULL
		  AND subscription_status NOT IN ('active', 'expired')
		  AND account_clear_at IS NOT NULL
		  AND account_clear_at < $1
	`, now)

	result, err := h.db.Exec(`
		UPDATE tenants SET deleted_at = NOW()
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
	c.JSON(http.StatusOK, gin.H{"success": true, "tenants_archived": rows})
}
