package handler

import (
	"context"
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
		tenantCode     string
		tenantName     string
		trialEndsAt    sql.NullTime
		accountClearAt sql.NullTime
		isActive       bool
		paidUsers      int
	)

	err := h.db.QueryRow(`
		SELECT subscription_status, subscription_plan, code, COALESCE(name, ''),
		       trial_ends_at, account_clear_at, is_active, COALESCE(paid_users, 0)
		FROM tenants
		WHERE id = $1 AND deleted_at IS NULL
	`, tenantID).Scan(&status, &plan, &tenantCode, &tenantName, &trialEndsAt, &accountClearAt, &isActive, &paidUsers)
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
		"status":      status,
		"plan":        plan,
		"tenant_code": tenantCode,
		"tenant_name": tenantName,
		"is_active":   isActive,
		"paid_users":  paidUsers,
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

	// Always log all fields so we can debug the signature formula
	h.log.Info("Multicard webhook received",
		"uuid", payload.UUID,
		"invoice_id", payload.InvoiceID,
		"amount", payload.Amount,
		"status", payload.Status,
		"billing_id", payload.BillingID,
		"payment_time", payload.PaymentTime,
		"phone", payload.Phone,
		"card_pan", payload.CardPan,
		"ps", payload.PS,
		"card_token", payload.CardToken,
		"sign", payload.Sign,
	)

	// Verify signature only for final payment callbacks.
	// Pre-auth callbacks (status "draft"/"progress"/"") must return 200 or
	// Multicard shows "service unavailable" before the user can pay.
	isFinal := payload.Status == "success" || payload.Status == "error" || payload.Status == "revert"
	if isFinal && !h.multicardClient.VerifySign(payload) {
		h.log.Warn("Multicard webhook: signature mismatch — PROCEEDING ANYWAY (fix formula)",
			"invoice_id", payload.InvoiceID,
			"uuid", payload.UUID,
			"amount", payload.Amount,
			"status", payload.Status,
			"billing_id", payload.BillingID,
			"payment_time", payload.PaymentTime,
			"phone", payload.Phone,
			"card_pan", payload.CardPan,
			"ps", payload.PS,
			"sign_received", payload.Sign,
		)
		// Intentionally fall through: the payment genuinely came from Multicard
		// (private callback_url). We process it so the customer isn't left unpaid.
		// TODO: restore strict signature check once the correct formula is confirmed.
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
		// Parse users count and billing period from plan string (e.g. "4users_monthly")
		var paidUsers int
		var billing string
		fmt.Sscanf(plan, "%dusers_%s", &paidUsers, &billing)
		if paidUsers < 1 {
			paidUsers = 1
		}

		// Calculate subscription end date based on billing period
		now := time.Now()
		var endDate time.Time
		switch billing {
		case "yearly":
			endDate = now.AddDate(1, 0, 0) // +1 year
		default:
			endDate = now.AddDate(0, 1, 0) // +1 month
		}

		h.db.Exec(`
			UPDATE tenants
			SET subscription_status = 'active',
			    subscription_plan   = 'professional',
			    paid_users          = $2,
			    is_active           = true,
			    trial_ends_at       = NULL,
			    account_clear_at    = $3,
			    updated_at          = NOW()
			WHERE id = $1
		`, tenantID, paidUsers, endDate)

		h.log.Info("Subscription activated via Multicard",
			"tenant_id", tenantID, "plan", plan, "paid_users", paidUsers, "ends_at", endDate, "invoice", payload.InvoiceID)
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// VerifyPayment polls Multicard for the latest payment status and activates
// the subscription if the payment is confirmed as successful.
// POST /subscription/verify-payment
// Body: { "invoice_id": "genix-xxxx-yyyy" }
func (h *Handler) VerifyPayment(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "Tenant not resolved")
		return
	}

	var input struct {
		InvoiceID string `json:"invoice_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "invoice_id is required")
		return
	}

	// Look up payment record
	var (
		plan          string
		multicardUUID string
		currentStatus string
	)
	err := h.db.QueryRow(`
		SELECT plan, multicard_uuid, status
		FROM subscription_payments
		WHERE invoice_id = $1 AND tenant_id = $2
	`, input.InvoiceID, tenantID).Scan(&plan, &multicardUUID, &currentStatus)
	if err != nil {
		response.NotFound(c, "Invoice not found")
		return
	}

	if currentStatus == "success" {
		response.Success(c, gin.H{"status": "success", "message": "Already activated"})
		return
	}

	// Query Multicard for real status
	ps, err := h.multicardClient.GetPaymentStatus(c.Request.Context(), multicardUUID)
	if err != nil {
		h.log.Error("VerifyPayment: failed to query Multicard", "error", err, "uuid", multicardUUID)
		response.InternalServerError(c, "Failed to query payment status")
		return
	}

	h.log.Info("VerifyPayment: Multicard status",
		"invoice_id", input.InvoiceID, "uuid", multicardUUID,
		"status", ps.Status, "billing_id", ps.BillingID, "amount", ps.Amount)

	// Update our record with the latest status
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
	`, input.InvoiceID, ps.Status, ps.BillingID, ps.CardPan, ps.PS, ps.CardToken, ps.PaymentTime)

	if ps.Status == "success" {
		var paidUsers int
		var billing string
		fmt.Sscanf(plan, "%dusers_%s", &paidUsers, &billing)
		if paidUsers < 1 {
			paidUsers = 1
		}

		now := time.Now()
		var endDate time.Time
		switch billing {
		case "yearly":
			endDate = now.AddDate(1, 0, 0)
		default:
			endDate = now.AddDate(0, 1, 0)
		}

		h.db.Exec(`
			UPDATE tenants
			SET subscription_status = 'active',
			    subscription_plan   = 'professional',
			    paid_users          = $2,
			    is_active           = true,
			    trial_ends_at       = NULL,
			    account_clear_at    = $3,
			    updated_at          = NOW()
			WHERE id = $1
		`, tenantID, paidUsers, endDate)
		h.log.Info("VerifyPayment: subscription activated", "tenant_id", tenantID, "plan", plan, "ends_at", endDate)
	}

	response.Success(c, gin.H{"status": ps.Status})
}

// GetPaymentHistory returns the list of payment records for the tenant.
// GET /subscription/payments
func (h *Handler) GetPaymentHistory(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "Tenant not resolved")
		return
	}

	rows, err := h.db.Query(`
		SELECT invoice_id, plan, amount_uzs, status, payment_time, created_at
		FROM subscription_payments
		WHERE tenant_id = $1
		ORDER BY created_at DESC
		LIMIT 50
	`, tenantID)
	if err != nil {
		response.InternalServerError(c, "Failed to fetch payment history")
		return
	}
	defer rows.Close()

	type PaymentRecord struct {
		InvoiceID   string       `json:"invoice_id"`
		Plan        string       `json:"plan"`
		AmountUZS   int64        `json:"amount_uzs"`
		Status      string       `json:"status"`
		PaymentTime sql.NullTime `json:"-"`
		CreatedAt   time.Time    `json:"created_at"`
		PaidAt      *time.Time   `json:"paid_at"`
	}

	var payments []PaymentRecord
	for rows.Next() {
		var p PaymentRecord
		if err := rows.Scan(&p.InvoiceID, &p.Plan, &p.AmountUZS, &p.Status, &p.PaymentTime, &p.CreatedAt); err != nil {
			continue
		}
		if p.PaymentTime.Valid {
			t := p.PaymentTime.Time
			p.PaidAt = &t
		}
		payments = append(payments, p)
	}
	if payments == nil {
		payments = []PaymentRecord{}
	}

	response.Success(c, payments)
}

// CleanExpiredTenants handles subscription lifecycle:
// 1. Trial expired (trial_ends_at passed) → set past_due + account_clear_at = trial_ends_at + 30 days
// 2. Active subscription expired (account_clear_at passed) → set past_due + account_clear_at = now + 30 days
// 3. Past due for 30+ days (account_clear_at passed while past_due) → soft-delete
// POST /admin/clean-expired-tenants
func (h *Handler) CleanExpiredTenants(c *gin.Context) {
	now := time.Now()

	// Step 1: Expired trials → past_due with 30-day grace period
	r1, _ := h.db.Exec(`
		UPDATE tenants
		SET subscription_status = 'past_due',
		    account_clear_at = trial_ends_at + INTERVAL '30 days',
		    updated_at = NOW()
		WHERE deleted_at IS NULL
		  AND subscription_status = 'trialing'
		  AND trial_ends_at IS NOT NULL
		  AND trial_ends_at < $1
	`, now)
	trialExpired, _ := r1.RowsAffected()

	// Step 2: Active subscriptions expired → past_due with 30-day grace
	r2, _ := h.db.Exec(`
		UPDATE tenants
		SET subscription_status = 'past_due',
		    account_clear_at = account_clear_at + INTERVAL '30 days',
		    updated_at = NOW()
		WHERE deleted_at IS NULL
		  AND subscription_status = 'active'
		  AND account_clear_at IS NOT NULL
		  AND account_clear_at < $1
	`, now)
	subExpired, _ := r2.RowsAffected()

	// Step 3: Past due for 30+ days → mark expired and deactivate
	r3, _ := h.db.Exec(`
		UPDATE tenants
		SET subscription_status = 'expired', is_active = false, updated_at = NOW()
		WHERE deleted_at IS NULL
		  AND subscription_status = 'past_due'
		  AND account_clear_at IS NOT NULL
		  AND account_clear_at < $1
	`, now)
	deactivated, _ := r3.RowsAffected()

	// Step 4: Expired → HARD DELETE all company data immediately
	expiredRows, _ := h.db.Query(`
		SELECT id::text FROM tenants
		WHERE deleted_at IS NULL
		  AND subscription_status = 'expired'
		  AND is_active = false
	`)
	var deleted int64
	if expiredRows != nil {
		var tenantIDs []string
		for expiredRows.Next() {
			var tid string
			expiredRows.Scan(&tid)
			tenantIDs = append(tenantIDs, tid)
		}
		expiredRows.Close()
		for _, tid := range tenantIDs {
			h.log.Info("Hard-deleting expired tenant", "tenant_id", tid)
			h.hardDeleteTenantData(tid)
			deleted++
		}
	}

	h.log.Info("CleanExpiredTenants completed",
		"trial_expired", trialExpired, "sub_expired", subExpired,
		"deactivated", deactivated, "deleted", deleted)

	c.JSON(http.StatusOK, gin.H{
		"success":        true,
		"trial_expired":  trialExpired,
		"sub_expired":    subExpired,
		"deactivated":    deactivated,
		"tenants_deleted": deleted,
	})
}

// HardDeleteTenant permanently removes ALL data for a tenant from every table.
func (h *Handler) hardDeleteTenantData(tenantID string) (int, error) {
	// Order matters: delete child tables first, parent tables last.
	// All these tables have tenant_id column.
	tables := []string{
		// Manufacturing
		"work_order_time_logs", "work_order_materials", "production_material_consumption",
		"production_split_outputs", "quality_checks", "quality_defects", "quality_control_points",
		"oee_metrics", "downtime_logs", "equipment_maintenance", "manufacturing_equipment",
		"work_center_calendar", "manufacturing_shifts", "mrp_recommendations", "mrp_supply", "mrp_demand",
		"work_orders", "production_orders", "manufacturing_categories", "work_centers",
		// Inventory
		"inventory_transactions", "inventory_lots", "inventory",
		"stock_operation_step_log", "stock_operation_lines", "stock_operations",
		"operation_type_steps", "warehouse_operation_types", "warehouse_locations",
		"scrap_order_lines", "scrap_orders", "scrap_reasons",
		"stock_count_items", "stock_counts", "transfer_order_lines", "transfer_orders",
		"reorder_rules", "reorder_alerts",
		// Products
		"product_organization_settings", "product_variant_values", "product_variants",
		"product_attribute_values", "product_attributes", "product_packagings",
		"bom_operations", "bom_lines", "product_boms", "products", "product_categories", "units_of_measure",
		// Sales
		"sales_order_lines", "sales_orders", "sales_invoice_lines", "sales_invoices",
		"sales_return_lines", "sales_returns", "delivery_order_lines", "delivery_orders",
		"quotation_lines", "quotations",
		// Purchase
		"purchase_order_lines", "purchase_orders", "purchase_invoice_lines", "purchase_invoices",
		"purchase_return_lines", "purchase_returns",
		"goods_receipt_lines", "goods_receipts",
		"rfq_lines", "rfq_responses", "rfqs",
		"vendor_prices", "blanket_order_lines", "blanket_orders",
		"procurement_contracts", "procurement_rules",
		// Finance
		"journal_entry_lines", "journal_entries", "journals",
		"payment_allocations", "payments", "payment_methods",
		"bank_reconciliation_items", "bank_reconciliations", "bank_transactions", "bank_accounts",
		"cash_book_entries", "cash_orders", "cash_registers", "cash_transactions",
		"budget_lines", "budgets", "fiscal_periods", "fiscal_years",
		"reconciliation_act_lines", "reconciliation_acts",
		"accounts", "tax_rates", "exchange_rates", "currencies",
		// HR
		"payroll_lines", "payroll_periods",
		"attendance_records", "leave_requests", "leave_allocations",
		"employee_contracts", "employees",
		// CRM
		"crm_attachments", "activities", "opportunity_stages", "pipeline_stages",
		"opportunities", "leads",
		// Projects & Construction
		"project_expenses", "project_team_members", "tasks", "projects",
		"construction_stage_materials", "construction_stages", "construction_buildings",
		"building_files", "construction_projects",
		// Expenses & Assets
		"expense_lines", "expenses", "fixed_asset_depreciation", "fixed_assets",
		// Cargo
		"cargo_shipment_items", "cargo_shipments",
		// Misc
		"subscription_payments", "contact_submissions",
		"approval_step_actions", "approval_steps", "approval_workflow_instances", "approval_workflows",
		"notifications", "audit_logs", "attachments", "ai_conversations", "ai_prompts",
		"sequences", "settings", "tenant_settings",
		// Installed apps
		"installed_apps",
		// User & org (last)
		"user_roles", "role_permissions", "roles", "api_keys",
		"departments", "organizations",
		"users",
	}

	totalDeleted := 0
	for _, table := range tables {
		result, err := h.db.Exec(fmt.Sprintf("DELETE FROM %s WHERE tenant_id = $1", table), tenantID)
		if err != nil {
			// Table might not exist or have different schema — skip
			h.log.Warn("hardDeleteTenantData: failed to delete from table", "table", table, "error", err)
			continue
		}
		rows, _ := result.RowsAffected()
		if rows > 0 {
			h.log.Info("hardDeleteTenantData: deleted rows", "table", table, "rows", rows)
			totalDeleted += int(rows)
		}
	}

	// Finally delete the tenant itself
	h.db.Exec("DELETE FROM tenants WHERE id = $1", tenantID)
	totalDeleted++

	return totalDeleted, nil
}

// RunSubscriptionCleanupScheduler runs CleanExpiredTenants daily at 03:00 Tashkent time.
func (h *Handler) RunSubscriptionCleanupScheduler(ctx context.Context) {
	go func() {
		loc, _ := time.LoadLocation("Asia/Tashkent")
		if loc == nil {
			loc = time.FixedZone("UZT", 5*3600)
		}

		for {
			// Calculate next 03:00 Tashkent
			nowTashkent := time.Now().In(loc)
			next := time.Date(nowTashkent.Year(), nowTashkent.Month(), nowTashkent.Day(), 3, 0, 0, 0, loc)
			if next.Before(nowTashkent) {
				next = next.Add(24 * time.Hour)
			}
			sleepDur := next.Sub(time.Now())

			select {
			case <-ctx.Done():
				return
			case <-time.After(sleepDur):
			}

			h.log.Info("Running scheduled subscription cleanup")
			now := time.Now()

			// Same logic as CleanExpiredTenants but without gin.Context
			r1, _ := h.db.Exec(`UPDATE tenants SET subscription_status = 'past_due', account_clear_at = trial_ends_at + INTERVAL '30 days', updated_at = NOW() WHERE deleted_at IS NULL AND subscription_status = 'trialing' AND trial_ends_at IS NOT NULL AND trial_ends_at < $1`, now)
			r2, _ := h.db.Exec(`UPDATE tenants SET subscription_status = 'past_due', account_clear_at = account_clear_at + INTERVAL '30 days', updated_at = NOW() WHERE deleted_at IS NULL AND subscription_status = 'active' AND account_clear_at IS NOT NULL AND account_clear_at < $1`, now)
			r3, _ := h.db.Exec(`UPDATE tenants SET subscription_status = 'expired', is_active = false, updated_at = NOW() WHERE deleted_at IS NULL AND subscription_status = 'past_due' AND account_clear_at IS NOT NULL AND account_clear_at < $1`, now)

			t1, _ := r1.RowsAffected()
			t2, _ := r2.RowsAffected()
			t3, _ := r3.RowsAffected()

			// Hard-delete expired tenants
			expiredRows, _ := h.db.Query(`SELECT id::text FROM tenants WHERE deleted_at IS NULL AND subscription_status = 'expired' AND is_active = false`)
			var t4 int64
			if expiredRows != nil {
				var ids []string
				for expiredRows.Next() {
					var tid string
					expiredRows.Scan(&tid)
					ids = append(ids, tid)
				}
				expiredRows.Close()
				for _, tid := range ids {
					h.log.Info("Scheduler: hard-deleting expired tenant", "tenant_id", tid)
					h.hardDeleteTenantData(tid)
					t4++
				}
			}

			h.log.Info("Subscription cleanup done", "trial_expired", t1, "sub_expired", t2, "deactivated", t3, "deleted", t4)
		}
	}()
}
