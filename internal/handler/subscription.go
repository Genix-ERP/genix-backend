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

// GetPlans returns per-user pricing.
// GET /subscription/plans
func (h *Handler) GetPlans(c *gin.Context) {
	response.Success(c, gin.H{
		"price_per_user_monthly": h.config.Multicard.PricePerUserMonthly,
		"price_per_user_yearly":  h.config.Multicard.PricePerUserYearly,
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
// Body: { "users": 3, "billing": "monthly" | "yearly" }
func (h *Handler) CreateCheckout(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "Tenant not resolved")
		return
	}

	var input struct {
		Users   int    `json:"users" binding:"required,min=1"`
		Billing string `json:"billing" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "users (min 1) and billing (monthly|yearly) are required")
		return
	}
	if input.Billing != "monthly" && input.Billing != "yearly" {
		response.BadRequest(c, "billing must be 'monthly' or 'yearly'")
		return
	}

	var pricePerUser int64
	if input.Billing == "yearly" {
		pricePerUser = h.config.Multicard.PricePerUserYearly * 12 // yearly upfront
	} else {
		pricePerUser = h.config.Multicard.PricePerUserMonthly
	}
	amountUZS := pricePerUser * int64(input.Users)
	amountTiyin := amountUZS * 100

	invoiceID := fmt.Sprintf("genix-%s-%s", tenantID[:8], uuid.New().String()[:8])

	frontendURL := h.config.App.FrontendURL
	backendURL := h.config.App.BaseURL

	inv, err := h.multicardClient.CreateInvoice(c.Request.Context(), payment.InvoiceRequest{
		Amount:       amountTiyin,
		InvoiceID:    invoiceID,
		ReturnURL:    fmt.Sprintf("%s/payment-success?users=%d&billing=%s", frontendURL, input.Users, input.Billing),
		ReturnErrURL: frontendURL + "/payment-error",
		CallbackURL:  backendURL + "/api/v1/webhooks/multicard",
	})
	if err != nil {
		h.log.Error("Multicard create invoice failed", "error", err)
		response.InternalServerError(c, "Failed to create payment")
		return
	}

	// Record payment attempt — store users+billing in plan field as "Nusers_billing"
	plan := fmt.Sprintf("%dusers_%s", input.Users, input.Billing)
	if _, err := h.db.Exec(`
		INSERT INTO subscription_payments
			(tenant_id, plan, amount_uzs, amount_tiyin, invoice_id, multicard_uuid, checkout_url, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending')
	`, tenantID, plan, amountUZS, amountTiyin, invoiceID, inv.UUID, inv.CheckoutURL); err != nil {
		h.log.Error("CreateCheckout: failed to record payment attempt", "error", err, "invoice_id", invoiceID)
		response.InternalServerError(c, "Failed to record payment")
		return
	}

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
		// Parse users count from plan string (e.g. "4users_monthly")
		var paidUsers int
		var billing string
		fmt.Sscanf(plan, "%dusers_%s", &paidUsers, &billing)
		if paidUsers < 1 {
			paidUsers = 1
		}

		h.db.Exec(`
			UPDATE tenants
			SET subscription_status = 'active',
			    subscription_plan   = $2,
			    paid_users          = $3,
			    is_active           = true,
			    trial_ends_at       = NULL,
			    account_clear_at    = NULL,
			    updated_at          = NOW()
			WHERE id = $1
		`, tenantID, plan, paidUsers)

		h.log.Info("Subscription activated via Multicard",
			"tenant_id", tenantID, "plan", plan, "paid_users", paidUsers, "invoice", payload.InvoiceID)
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
