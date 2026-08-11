package handler

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// Recording a subscription payment that did not arrive through Multicard.
//
// Cash and bank transfers are a normal way for these customers to pay, and
// until now the platform had no place to put one. The subscription could be
// extended by hand (ActivateTenantSubscription), but the payment itself went
// unrecorded — so a company's payment history showed only its card payments,
// and "has this customer paid?" had no answer inside the system.
//
// The two halves are done together on purpose. Extending a subscription
// without recording the money produces exactly the gap described above;
// recording money without extending the subscription leaves a paying customer
// locked out. Either alone is a bug, so they share a transaction.

var manualPaymentMethods = map[string]bool{
	"cash":          true,
	"bank_transfer": true,
	"other":         true,
	// "multicard" is deliberately absent: those rows are written by the
	// payment callback, and letting them be typed in by hand would put
	// unverified rows next to verified ones with no way to tell them apart.
}

type recordManualPaymentInput struct {
	AmountUZS int64  `json:"amount_uzs" binding:"required,gt=0"`
	Method    string `json:"method" binding:"required"`
	PlanCode  string `json:"plan_code"`
	Months    int    `json:"months"`
	PaidUsers int    `json:"paid_users"`
	Note      string `json:"note"`
	// ExtendSubscription is opt-out rather than opt-in: the overwhelmingly
	// common case is money received for a period of service, and silently
	// taking payment without granting the period is the worse mistake.
	ExtendSubscription *bool `json:"extend_subscription"`
}

// RecordManualSubscriptionPayment godoc
// @Summary Record a cash or bank-transfer subscription payment
// @Tags Platform - Admin
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /admin/tenants/{id}/payments [post]
func (h *Handler) RecordManualSubscriptionPayment(c *gin.Context) {
	tenantID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid tenant ID")
		return
	}

	var in recordManualPaymentInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Summa va to'lov turi majburiy")
		return
	}

	in.Method = strings.ToLower(strings.TrimSpace(in.Method))
	if !manualPaymentMethods[in.Method] {
		response.BadRequest(c, "To'lov turi noto'g'ri — cash, bank_transfer yoki other")
		return
	}
	if in.Months < 0 {
		in.Months = 0
	}
	if in.PaidUsers < 0 {
		in.PaidUsers = 0
	}

	// The company must exist, and its current plan is the default when the
	// caller does not name one — recording a payment should not quietly move a
	// customer onto a different tariff.
	var currentPlan, tenantName string
	var currentPaidUsers int
	if err := h.db.QueryRow(`
		SELECT COALESCE(subscription_plan, 'free'), COALESCE(name, ''), COALESCE(paid_users, 0)
		FROM tenants WHERE id = $1`, tenantID).Scan(&currentPlan, &tenantName, &currentPaidUsers); err != nil {
		response.NotFound(c, "Tenant")
		return
	}
	if in.PlanCode == "" {
		in.PlanCode = currentPlan
	}
	if in.PaidUsers == 0 {
		in.PaidUsers = currentPaidUsers
	}
	if in.PaidUsers < 1 {
		in.PaidUsers = 1
	}

	extend := in.Months > 0
	if in.ExtendSubscription != nil {
		extend = *in.ExtendSubscription && in.Months > 0
	}

	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to begin manual payment tx", "error", err)
		response.InternalError(c, "To'lovni saqlab bo'lmadi")
		return
	}
	defer tx.Rollback()

	paymentID := uuid.New()
	// Synthesised so the UNIQUE constraint that makes Multicard callbacks
	// idempotent keeps working, and so a manual row is identifiable at a
	// glance in the raw table.
	invoiceID := fmt.Sprintf("MANUAL-%s", paymentID.String())

	var recordedBy interface{}
	if claims, _ := middleware.GetClaims(c); claims != nil && claims.PlatformUserID != nil {
		recordedBy = *claims.PlatformUserID
	}

	if _, err := tx.Exec(`
		INSERT INTO subscription_payments (
			id, tenant_id, plan, amount_uzs, amount_tiyin, invoice_id,
			status, method, note, recorded_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'success', $7, $8, $9, NOW(), NOW())`,
		paymentID, tenantID, in.PlanCode, in.AmountUZS, in.AmountUZS*100, invoiceID,
		in.Method, strings.TrimSpace(in.Note), recordedBy,
	); err != nil {
		h.log.Error("Failed to record manual subscription payment", "error", err, "tenant_id", tenantID)
		response.InternalError(c, "To'lovni saqlab bo'lmadi")
		return
	}

	var endsAt *time.Time
	if extend {
		// Extend from whichever is later: today, or the end of the period
		// already paid for. Extending from today would silently discard the
		// remainder a customer who pays early has already bought.
		var newEnd time.Time
		if err := tx.QueryRow(`
			UPDATE tenants
			SET subscription_status = 'active',
			    subscription_plan   = $1,
			    paid_users          = $2,
			    is_active           = true,
			    trial_ends_at       = NULL,
			    account_clear_at    = GREATEST(COALESCE(account_clear_at, NOW()), NOW())
			                          + ($3 || ' months')::interval,
			    updated_at          = NOW()
			WHERE id = $4
			RETURNING account_clear_at`,
			in.PlanCode, in.PaidUsers, in.Months, tenantID,
		).Scan(&newEnd); err != nil {
			h.log.Error("Failed to extend subscription for manual payment", "error", err, "tenant_id", tenantID)
			response.InternalError(c, "Obunani uzaytirib bo'lmadi")
			return
		}
		endsAt = &newEnd
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit manual payment", "error", err)
		response.InternalError(c, "To'lovni saqlab bo'lmadi")
		return
	}

	h.writePlatformAudit(c, "tenant.payment.manual", "tenant", tenantID.String(), &tenantID, nil,
		map[string]interface{}{
			"amount_uzs": in.AmountUZS, "method": in.Method, "plan_code": in.PlanCode,
			"months": in.Months, "paid_users": in.PaidUsers, "extended": extend,
			"payment_id": paymentID.String(),
		})

	out := gin.H{
		"id":         paymentID,
		"amount_uzs": in.AmountUZS,
		"method":     in.Method,
		"plan_code":  in.PlanCode,
		"extended":   extend,
	}
	if endsAt != nil {
		out["ends_at"] = *endsAt
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": out})
}
