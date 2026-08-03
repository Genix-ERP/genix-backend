package handler

import (
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Odoo-style partner reconciliation (Moliya → Qarzdorlik → Solishtirish).
// A confirmed payment's money that is not yet allocated to any document is
// the partner's "credit"; these endpoints list it and apply it to open
// invoices/bills. No journal entries are posted here — the payment's JE was
// posted at confirm time; allocation only moves the subledger
// (payment_allocations + invoice amount_paid/status), mirroring
// ConfirmPayment's update logic.

type reconcileDirection struct {
	docTable   string // invoice table
	contactCol string // partner FK column on the invoice table
	docType    string // payment_allocations.document_type
	payType    string // payments.type
}

func resolveReconcileDirection(direction string) (reconcileDirection, bool) {
	switch direction {
	case "customer", "":
		return reconcileDirection{"sales_invoices", "customer_id", "sales_invoice", "receipt"}, true
	case "vendor":
		return reconcileDirection{"purchase_invoices", "vendor_id", "purchase_invoice", "payment"}, true
	}
	return reconcileDirection{}, false
}

// GetPartnerBalances lists each partner's invoiced/paid/due totals plus
// unallocated confirmed-payment credit for one direction (customer|vendor).
func (h *Handler) GetPartnerBalances(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	var orgArg interface{}
	if orgID, okOrg := middleware.GetOrganizationID(c); okOrg && orgID != uuid.Nil {
		orgArg = orgID
	}
	dir, okDir := resolveReconcileDirection(c.Query("direction"))
	if !okDir {
		response.BadRequest(c, "direction must be customer or vendor")
		return
	}

	rows, err := h.db.Query(`
		WITH inv AS (
			SELECT `+dir.contactCol+` AS contact_id,
			       COALESCE(SUM(total_amount), 0) AS invoiced,
			       COALESCE(SUM(amount_paid), 0)  AS paid,
			       COALESCE(SUM(amount_due), 0)   AS due
			FROM `+dir.docTable+`
			WHERE tenant_id = $1 AND deleted_at IS NULL
			  AND status NOT IN ('draft', 'cancelled', 'void')
			  AND ($2::uuid IS NULL OR organization_id = $2)
			GROUP BY 1
		),
		cred AS (
			SELECT p.contact_id,
			       SUM(p.amount - COALESCE(a.allocated, 0)) AS credit
			FROM payments p
			LEFT JOIN (
				SELECT payment_id, SUM(amount) AS allocated
				FROM payment_allocations GROUP BY payment_id
			) a ON a.payment_id = p.id
			WHERE p.tenant_id = $1 AND p.deleted_at IS NULL
			  AND p.type = $3 AND p.status = 'confirmed'
			  AND ($2::uuid IS NULL OR p.organization_id = $2)
			GROUP BY p.contact_id
		)
		SELECT c.id, c.name,
		       COALESCE(i.invoiced, 0), COALESCE(i.paid, 0), COALESCE(i.due, 0),
		       GREATEST(COALESCE(cr.credit, 0), 0)
		FROM contacts c
		LEFT JOIN inv i ON i.contact_id = c.id
		LEFT JOIN cred cr ON cr.contact_id = c.id
		WHERE c.tenant_id = $1 AND c.deleted_at IS NULL
		  AND (COALESCE(i.invoiced, 0) > 0 OR COALESCE(cr.credit, 0) > 0.001)
		ORDER BY COALESCE(i.due, 0) DESC, GREATEST(COALESCE(cr.credit, 0), 0) DESC, c.name
		LIMIT 500
	`, tenantID, orgArg, dir.payType)
	if err != nil {
		h.log.Error("Failed to list partner balances", "error", err)
		response.InternalError(c, "Failed to list partner balances")
		return
	}
	defer rows.Close()

	type partnerBalance struct {
		ContactID uuid.UUID `json:"contact_id"`
		Name      string    `json:"name"`
		Invoiced  float64   `json:"invoiced"`
		Paid      float64   `json:"paid"`
		Due       float64   `json:"due"`
		Credit    float64   `json:"credit"`
	}
	balances := []partnerBalance{}
	for rows.Next() {
		var b partnerBalance
		if rows.Scan(&b.ContactID, &b.Name, &b.Invoiced, &b.Paid, &b.Due, &b.Credit) == nil {
			balances = append(balances, b)
		}
	}
	response.Success(c, balances)
}

// GetPartnerLedger returns one partner's open documents, available credit and
// the confirmed payments carrying that credit.
func (h *Handler) GetPartnerLedger(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	var orgArg interface{}
	if orgID, okOrg := middleware.GetOrganizationID(c); okOrg && orgID != uuid.Nil {
		orgArg = orgID
	}
	contactID, err := uuid.Parse(c.Query("contact_id"))
	if err != nil {
		response.BadRequest(c, "Invalid contact_id")
		return
	}
	dir, okDir := resolveReconcileDirection(c.Query("direction"))
	if !okDir {
		response.BadRequest(c, "direction must be customer or vendor")
		return
	}

	type openDoc struct {
		ID            uuid.UUID `json:"id"`
		InvoiceNumber string    `json:"invoice_number"`
		InvoiceDate   string    `json:"invoice_date"`
		TotalAmount   float64   `json:"total_amount"`
		Due           float64   `json:"due"`
	}
	openDocs := []openDoc{}
	docRows, err := h.db.Query(`
		SELECT id, invoice_number, invoice_date, COALESCE(total_amount, 0), COALESCE(amount_due, 0)
		FROM `+dir.docTable+`
		WHERE tenant_id = $1 AND deleted_at IS NULL AND `+dir.contactCol+` = $2
		  AND status NOT IN ('draft', 'cancelled', 'void') AND amount_due > 0.001
		  AND ($3::uuid IS NULL OR organization_id = $3)
		ORDER BY invoice_date ASC, created_at ASC
	`, tenantID, contactID, orgArg)
	if err != nil {
		h.log.Error("Failed to list open documents", "error", err)
		response.InternalError(c, "Failed to load partner ledger")
		return
	}
	for docRows.Next() {
		var d openDoc
		var invDate time.Time
		if docRows.Scan(&d.ID, &d.InvoiceNumber, &invDate, &d.TotalAmount, &d.Due) == nil {
			d.InvoiceDate = invDate.Format("2006-01-02")
			openDocs = append(openDocs, d)
		}
	}
	docRows.Close()

	type creditPayment struct {
		ID            uuid.UUID `json:"id"`
		PaymentNumber string    `json:"payment_number"`
		PaymentDate   string    `json:"payment_date"`
		Amount        float64   `json:"amount"`
		Unallocated   float64   `json:"unallocated"`
	}
	payments := []creditPayment{}
	creditAvailable := 0.0
	payRows, err := h.db.Query(`
		SELECT p.id, p.payment_number, p.payment_date, p.amount,
		       p.amount - COALESCE(a.allocated, 0) AS unallocated
		FROM payments p
		LEFT JOIN (
			SELECT payment_id, SUM(amount) AS allocated
			FROM payment_allocations GROUP BY payment_id
		) a ON a.payment_id = p.id
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL AND p.contact_id = $2
		  AND p.type = $3 AND p.status = 'confirmed'
		  AND p.amount - COALESCE(a.allocated, 0) > 0.001
		  AND ($4::uuid IS NULL OR p.organization_id = $4)
		ORDER BY p.payment_date ASC, p.created_at ASC
	`, tenantID, contactID, dir.payType, orgArg)
	if err != nil {
		h.log.Error("Failed to list credit payments", "error", err)
		response.InternalError(c, "Failed to load partner ledger")
		return
	}
	for payRows.Next() {
		var p creditPayment
		var payDate time.Time
		if payRows.Scan(&p.ID, &p.PaymentNumber, &payDate, &p.Amount, &p.Unallocated) == nil {
			p.PaymentDate = payDate.Format("2006-01-02")
			payments = append(payments, p)
			creditAvailable += p.Unallocated
		}
	}
	payRows.Close()

	response.Success(c, gin.H{
		"contact_id":       contactID,
		"credit_available": creditAvailable,
		"open_docs":        openDocs,
		"payments":         payments,
	})
}

// ReconcilePartnerCredit applies a partner's unallocated confirmed-payment
// credit to one open document. amount <= 0 means "as much as possible".
// Atomic: allocation rows + invoice amount_paid/status in one tx.
func (h *Handler) ReconcilePartnerCredit(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	var orgArg interface{}
	if orgID, okOrg := middleware.GetOrganizationID(c); okOrg && orgID != uuid.Nil {
		orgArg = orgID
	}

	var input struct {
		ContactID  string  `json:"contact_id" binding:"required"`
		Direction  string  `json:"direction"`
		DocumentID string  `json:"document_id" binding:"required"`
		Amount     float64 `json:"amount"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	contactID, err := uuid.Parse(input.ContactID)
	if err != nil {
		response.BadRequest(c, "Invalid contact_id")
		return
	}
	documentID, err := uuid.Parse(input.DocumentID)
	if err != nil {
		response.BadRequest(c, "Invalid document_id")
		return
	}
	dir, okDir := resolveReconcileDirection(input.Direction)
	if !okDir {
		response.BadRequest(c, "direction must be customer or vendor")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	// Lock the target document; verify partner + tenant ownership.
	var due, totalAmount, amountPaid float64
	err = tx.QueryRow(`
		SELECT COALESCE(amount_due, 0), COALESCE(total_amount, 0), COALESCE(amount_paid, 0)
		FROM `+dir.docTable+`
		WHERE id = $1 AND tenant_id = $2 AND `+dir.contactCol+` = $3
		  AND deleted_at IS NULL AND status NOT IN ('draft', 'cancelled', 'void')
		  AND ($4::uuid IS NULL OR organization_id = $4)
		FOR UPDATE
	`, documentID, tenantID, contactID, orgArg).Scan(&due, &totalAmount, &amountPaid)
	if err != nil {
		response.BadRequest(c, "Open document not found for this partner")
		return
	}
	if due <= 0.001 {
		response.Success(c, gin.H{"applied": 0, "fully_paid": true})
		return
	}

	// Lock the partner's confirmed payments and compute unallocated remainders
	// (collect first — lib/pq can't interleave queries on one connection).
	type creditRow struct {
		ID          uuid.UUID
		Unallocated float64
	}
	var credits []creditRow
	payRows, err := tx.Query(`
		SELECT p.id, p.amount - COALESCE((
			SELECT SUM(pa.amount) FROM payment_allocations pa WHERE pa.payment_id = p.id
		), 0) AS unallocated
		FROM payments p
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL AND p.contact_id = $2
		  AND p.type = $3 AND p.status = 'confirmed'
		  AND ($4::uuid IS NULL OR p.organization_id = $4)
		ORDER BY p.payment_date ASC, p.created_at ASC
		FOR UPDATE OF p
	`, tenantID, contactID, dir.payType, orgArg)
	if err != nil {
		h.log.Error("Failed to lock credit payments", "error", err)
		response.InternalError(c, "Failed to reconcile")
		return
	}
	for payRows.Next() {
		var cr creditRow
		if payRows.Scan(&cr.ID, &cr.Unallocated) == nil && cr.Unallocated > 0.001 {
			credits = append(credits, cr)
		}
	}
	payRows.Close()

	target := due
	if input.Amount > 0 && input.Amount < target {
		target = input.Amount
	}

	now := time.Now()
	applied := 0.0
	for _, cr := range credits {
		if target-applied <= 0.001 {
			break
		}
		chunk := target - applied
		if cr.Unallocated < chunk {
			chunk = cr.Unallocated
		}
		if _, err := tx.Exec(`
			INSERT INTO payment_allocations (id, payment_id, document_type, document_id, amount, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, uuid.New(), cr.ID, dir.docType, documentID, chunk, now); err != nil {
			h.log.Error("Failed to insert allocation", "error", err)
			response.InternalError(c, "Failed to reconcile")
			return
		}
		applied += chunk
	}

	if applied <= 0.001 {
		response.Success(c, gin.H{"applied": 0, "fully_paid": false})
		return
	}

	// Same status transition as ConfirmPayment's allocation handling.
	statusSQL := `
		UPDATE ` + dir.docTable + ` SET
			amount_paid = amount_paid + $1,
			status = CASE WHEN amount_paid + $1 >= total_amount - 0.001 THEN 'paid' ELSE 'partial' END,
			updated_at = $2
		WHERE id = $3 AND tenant_id = $4`
	if dir.docTable == "purchase_invoices" {
		statusSQL = `
		UPDATE purchase_invoices SET
			amount_paid = amount_paid + $1,
			status = CASE WHEN amount_paid + $1 >= total_amount - 0.001 THEN 'paid' ELSE 'partial' END,
			payment_status = CASE WHEN amount_paid + $1 >= total_amount - 0.001 THEN 'paid' ELSE 'partial' END,
			updated_at = $2
		WHERE id = $3 AND tenant_id = $4`
	}
	if _, err := tx.Exec(statusSQL, applied, now, documentID, tenantID); err != nil {
		h.log.Error("Failed to update document after reconcile", "error", err)
		response.InternalError(c, "Failed to reconcile")
		return
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit reconcile", "error", err)
		response.InternalError(c, "Failed to reconcile")
		return
	}

	response.Success(c, gin.H{
		"applied":    applied,
		"fully_paid": amountPaid+applied >= totalAmount-0.001,
	})
}
