package handler

// Partner payment register + reconciliation endpoints (Odoo-style).
// Ported from the yuksalish branch so the Reconcile UI (already on main via the
// dev merge) has its backend: register a payment for a partner without picking
// an invoice, list partner balances, drill one partner's ledger, and apply a
// partner's unallocated credit to an open document. Depends only on
// findAccount() (chart-of-accounts lookup), which already exists on main.

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

func (h *Handler) RegisterPartnerPayment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	orgID, _ := middleware.GetOrganizationID(c)
	var orgPtr *uuid.UUID
	var orgArg interface{}
	if orgID != uuid.Nil {
		orgPtr = &orgID
		orgArg = orgID
	}

	var input struct {
		ContactID   string  `json:"contact_id" binding:"required"`
		Amount      float64 `json:"amount" binding:"required,gt=0"`
		Direction   string  `json:"direction" binding:"required,oneof=customer vendor"`
		PaymentDate string  `json:"payment_date,omitempty"`
		Method      string  `json:"method,omitempty"` // cash | bank
		Notes       string  `json:"notes,omitempty"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}
	contactID, err := uuid.Parse(input.ContactID)
	if err != nil {
		response.BadRequest(c, "Invalid contact_id")
		return
	}
	now := time.Now()
	if input.PaymentDate != "" {
		if d, e := time.Parse("2006-01-02", input.PaymentDate); e == nil {
			now = d
		}
	}
	isCustomer := input.Direction == "customer"

	// Resolve the cash/bank account.
	var cashAcct uuid.UUID
	if input.Method == "bank" {
		cashAcct = findAccount(h.db, tenantID, orgPtr, "bank", "5110")
		if cashAcct == uuid.Nil {
			cashAcct = findAccount(h.db, tenantID, orgPtr, "kassa", "5010")
		}
	} else {
		cashAcct = findAccount(h.db, tenantID, orgPtr, "kassa", "5010")
		if cashAcct == uuid.Nil {
			cashAcct = findAccount(h.db, tenantID, orgPtr, "bank", "5110")
		}
	}
	// Resolve the partner control account (prefer the contact's default).
	var partnerAcct uuid.UUID
	if isCustomer {
		var s sql.NullString
		_ = h.db.QueryRow(`SELECT default_receivable_account_id::text FROM contacts WHERE id=$1 AND tenant_id=$2`, contactID, tenantID).Scan(&s)
		if s.Valid && s.String != "" {
			partnerAcct, _ = uuid.Parse(s.String)
		}
		if partnerAcct == uuid.Nil {
			partnerAcct = findAccount(h.db, tenantID, orgPtr, "debitor", "4010")
		}
		if partnerAcct == uuid.Nil {
			partnerAcct = findAccount(h.db, tenantID, orgPtr, "receivable", "4010")
		}
	} else {
		var s sql.NullString
		_ = h.db.QueryRow(`SELECT default_payable_account_id::text FROM contacts WHERE id=$1 AND tenant_id=$2`, contactID, tenantID).Scan(&s)
		if s.Valid && s.String != "" {
			partnerAcct, _ = uuid.Parse(s.String)
		}
		if partnerAcct == uuid.Nil {
			partnerAcct = findAccount(h.db, tenantID, orgPtr, "kreditor", "6010")
		}
		if partnerAcct == uuid.Nil {
			partnerAcct = findAccount(h.db, tenantID, orgPtr, "payable", "6010")
		}
	}
	if cashAcct == uuid.Nil || partnerAcct == uuid.Nil {
		response.BadRequest(c, "Could not resolve the cash or partner (AR/AP) account — configure the chart of accounts first.")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to register payment")
		return
	}
	defer tx.Rollback()

	// Pick a journal (cash, then GENERAL, then any), scoped to org.
	var journalID uuid.UUID
	var prefix sql.NullString
	_ = tx.QueryRow(`SELECT id, number_prefix FROM journals
		WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2 OR organization_id IS NULL)
		ORDER BY CASE WHEN LOWER(COALESCE(type,''))='cash' THEN 0 WHEN code='GENERAL' THEN 1 ELSE 2 END LIMIT 1`,
		tenantID, orgArg).Scan(&journalID, &prefix)
	if journalID == uuid.Nil {
		response.BadRequest(c, "No journal is configured for this company.")
		return
	}
	var maxNum int
	_ = tx.QueryRow(`SELECT COALESCE(MAX(CAST(NULLIF(REGEXP_REPLACE(entry_number,'[^0-9]','','g'),'') AS BIGINT)),0)
		FROM journal_entries WHERE tenant_id=$1 AND journal_id=$2 AND deleted_at IS NULL
		  AND LENGTH(REGEXP_REPLACE(entry_number,'[^0-9]','','g')) <= 9`, tenantID, journalID).Scan(&maxNum)
	px := ""
	if prefix.Valid {
		px = prefix.String
	}
	entryNumber := fmt.Sprintf("%s%06d", px, maxNum+1)
	pType := "receipt"
	if !isCustomer {
		pType = "payment"
	}
	desc := fmt.Sprintf("%s payment", input.Direction)

	// Journal entry header.
	jeID := uuid.New()
	if _, err := tx.Exec(`INSERT INTO journal_entries
		(id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description, source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'payment',$9,1.0,$10,$10,'posted',$11,$12,$12)`,
		jeID, tenantID, orgArg, journalID, entryNumber, now, nullIfEmpty(input.Notes), desc, contactID.String(), input.Amount, userID, now); err != nil {
		h.log.Error("register payment: JE header", "error", err)
		response.InternalError(c, "Failed to post payment")
		return
	}
	jelInsert := func(acct uuid.UUID, contact interface{}, line int, debit, credit float64) error {
		_, e := tx.Exec(`INSERT INTO journal_entry_lines
			(id, journal_entry_id, line_number, account_id, contact_id, description, debit_amount, credit_amount, exchange_rate, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,1.0,$9)`, uuid.New(), jeID, line, acct, contact, desc, debit, credit, now)
		return e
	}
	var e1, e2 error
	if isCustomer {
		e1 = jelInsert(cashAcct, nil, 1, input.Amount, 0)          // DR Cash
		e2 = jelInsert(partnerAcct, contactID, 2, 0, input.Amount) // CR AR (partner)
	} else {
		e1 = jelInsert(partnerAcct, contactID, 1, input.Amount, 0) // DR AP (partner)
		e2 = jelInsert(cashAcct, nil, 2, 0, input.Amount)          // CR Cash
	}
	if e1 != nil || e2 != nil {
		h.log.Error("register payment: JE lines", "e1", e1, "e2", e2)
		response.InternalError(c, "Failed to post payment lines")
		return
	}
	// Account balances (cash debit-normal; AR debit-normal; AP credit-normal).
	if isCustomer {
		tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at=$2 WHERE id=$3`, input.Amount, now, cashAcct)
		tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at=$2 WHERE id=$3`, input.Amount, now, partnerAcct)
	} else {
		tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at=$2 WHERE id=$3`, input.Amount, now, partnerAcct)
		tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at=$2 WHERE id=$3`, input.Amount, now, cashAcct)
	}
	tx.Exec(`UPDATE journals SET next_number = next_number + 1 WHERE id=$1`, journalID)

	// Payment record.
	paymentID := uuid.New()
	if _, err := tx.Exec(`INSERT INTO payments
		(id, tenant_id, organization_id, type, payment_number, contact_id, payment_date, amount, currency_id, exchange_rate, reference, notes, status, journal_entry_id, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULL,1.0,$9,$10,'confirmed',$11,$12,$13,$13)`,
		paymentID, tenantID, orgArg, pType, "PAY-"+entryNumber, contactID, now, input.Amount,
		nullIfEmpty(input.Notes), nullIfEmpty(input.Notes), jeID, userID, now); err != nil {
		h.log.Error("register payment: payment row", "error", err)
		response.InternalError(c, "Failed to record payment")
		return
	}

	// FIFO-allocate to open invoices/bills, oldest first.
	table, contactCol := "sales_invoices", "customer_id"
	docType := "sales_invoice"
	if !isCustomer {
		table, contactCol, docType = "purchase_invoices", "vendor_id", "purchase_invoice"
	}
	type openInv struct {
		id     string
		number string
		due    float64
	}
	var opens []openInv
	rows, err := tx.Query(fmt.Sprintf(`SELECT id::text, invoice_number, total_amount - COALESCE(amount_paid,0) AS due
		FROM %s WHERE %s=$1 AND tenant_id=$2 AND deleted_at IS NULL
		  AND status NOT IN ('draft','cancelled') AND (total_amount - COALESCE(amount_paid,0)) > 0.001
		ORDER BY invoice_date ASC, created_at ASC`, table, contactCol), contactID, tenantID)
	if err != nil {
		h.log.Error("register payment: load open invoices", "error", err)
		response.InternalError(c, "Failed to allocate payment")
		return
	}
	for rows.Next() {
		var o openInv
		if rows.Scan(&o.id, &o.number, &o.due) == nil {
			opens = append(opens, o)
		}
	}
	rows.Close()

	remaining := input.Amount
	allocated := make([]map[string]interface{}, 0)
	for _, o := range opens {
		if remaining <= 0.001 {
			break
		}
		alloc := o.due
		if alloc > remaining {
			alloc = remaining
		}
		invUUID, _ := uuid.Parse(o.id)
		if _, err := tx.Exec(`INSERT INTO payment_allocations (id, payment_id, document_type, document_id, amount, created_at)
			VALUES ($1,$2,$3,$4,$5,$6)`, uuid.New(), paymentID, docType, invUUID, alloc, now); err != nil {
			h.log.Error("register payment: allocation", "error", err)
			response.InternalError(c, "Failed to allocate payment")
			return
		}
		if _, err := tx.Exec(fmt.Sprintf(`UPDATE %s
			SET amount_paid = COALESCE(amount_paid,0) + $1,
			    status = CASE WHEN COALESCE(amount_paid,0) + $1 >= total_amount - 0.001 THEN 'paid' ELSE 'partial' END,
			    updated_at = $2
			WHERE id = $3 AND tenant_id = $4`, table), alloc, now, invUUID, tenantID); err != nil {
			h.log.Error("register payment: update invoice", "error", err)
			response.InternalError(c, "Failed to settle invoice")
			return
		}
		allocated = append(allocated, map[string]interface{}{"invoice_number": o.number, "amount": alloc})
		remaining -= alloc
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("register payment: commit", "error", err)
		response.InternalError(c, "Failed to register payment")
		return
	}
	response.Success(c, gin.H{
		"payment_id":       paymentID,
		"amount":           input.Amount,
		"allocated":        allocated,
		"credit_remaining": remaining, // overpayment kept as a credit on the partner
	})
}

// partnerDocConfig maps a direction (customer|vendor) to its invoice table,
// contact column, payment type, and allocation document_type.
func partnerDocConfig(direction string) (invTable, contactCol, payType, docType string) {
	if direction == "vendor" {
		return "purchase_invoices", "vendor_id", "payment", "purchase_invoice"
	}
	return "sales_invoices", "customer_id", "receipt", "sales_invoice"
}

// GetPartnerBalances returns, per customer or vendor, their invoiced / paid /
// due totals plus any unallocated payment credit (Odoo-style partner list).
// Query: ?direction=customer|vendor
func (h *Handler) GetPartnerBalances(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	orgID, _ := middleware.GetOrganizationID(c)
	var orgArg interface{}
	if orgID != uuid.Nil {
		orgArg = orgID
	}
	direction := c.Query("direction")
	if direction != "vendor" {
		direction = "customer"
	}
	invTable, contactCol, payType, _ := partnerDocConfig(direction)

	q := fmt.Sprintf(`
		WITH inv AS (
			SELECT %s AS cid,
			       SUM(total_amount) AS invoiced,
			       SUM(COALESCE(amount_paid,0)) AS paid,
			       SUM(total_amount - COALESCE(amount_paid,0)) AS due
			FROM %s
			WHERE tenant_id=$1 AND deleted_at IS NULL AND status NOT IN ('draft','cancelled')
			  AND ($2::uuid IS NULL OR organization_id=$2)
			GROUP BY %s
		),
		pay AS (
			SELECT p.contact_id AS cid,
			       SUM(p.amount) AS ptotal,
			       COALESCE(SUM(al.alloc),0) AS allocated
			FROM payments p
			LEFT JOIN (SELECT payment_id, SUM(amount) AS alloc FROM payment_allocations GROUP BY payment_id) al ON al.payment_id=p.id
			WHERE p.tenant_id=$1 AND p.type=$3 AND p.status IN ('confirmed','posted')
			  AND ($2::uuid IS NULL OR p.organization_id=$2)
			GROUP BY p.contact_id
		)
		SELECT c.id::text, COALESCE(NULLIF(c.name,''),'-') AS name,
		       COALESCE(inv.invoiced,0), COALESCE(inv.paid,0), COALESCE(inv.due,0),
		       GREATEST(COALESCE(pay.ptotal,0)-COALESCE(pay.allocated,0),0) AS credit
		FROM contacts c
		LEFT JOIN inv ON inv.cid=c.id
		LEFT JOIN pay ON pay.cid=c.id
		WHERE c.tenant_id=$1 AND (inv.cid IS NOT NULL OR pay.cid IS NOT NULL)
		ORDER BY COALESCE(inv.due,0) DESC, name ASC`, contactCol, invTable, contactCol)

	rows, err := h.db.Query(q, tenantID, orgArg, payType)
	if err != nil {
		h.log.Error("partner balances", "error", err)
		response.InternalError(c, "Failed to load partner balances")
		return
	}
	defer rows.Close()
	out := make([]gin.H, 0)
	for rows.Next() {
		var id, name string
		var invoiced, paid, due, credit float64
		if rows.Scan(&id, &name, &invoiced, &paid, &due, &credit) == nil {
			out = append(out, gin.H{
				"contact_id": id, "name": name,
				"invoiced": invoiced, "paid": paid, "due": due,
				"credit": credit, "net_balance": due - credit,
			})
		}
	}
	response.Success(c, out)
}

// GetPartnerLedger returns one partner's open invoices/bills, their available
// (unallocated) payment credit, and the payments that make up that credit.
// Query: ?contact_id=&direction=customer|vendor
func (h *Handler) GetPartnerLedger(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	orgID, _ := middleware.GetOrganizationID(c)
	var orgArg interface{}
	if orgID != uuid.Nil {
		orgArg = orgID
	}
	contactID, err := uuid.Parse(c.Query("contact_id"))
	if err != nil {
		response.BadRequest(c, "Invalid contact_id")
		return
	}
	direction := c.Query("direction")
	if direction != "vendor" {
		direction = "customer"
	}
	invTable, contactCol, payType, _ := partnerDocConfig(direction)

	// Open documents (due > 0), oldest first.
	openDocs := make([]gin.H, 0)
	var totalDue float64
	drows, err := h.db.Query(fmt.Sprintf(`
		SELECT id::text, invoice_number, invoice_date,
		       total_amount, COALESCE(amount_paid,0),
		       total_amount - COALESCE(amount_paid,0) AS due, status
		FROM %s
		WHERE %s=$1 AND tenant_id=$2 AND deleted_at IS NULL
		  AND status NOT IN ('draft','cancelled')
		  AND (total_amount - COALESCE(amount_paid,0)) > 0.001
		  AND ($3::uuid IS NULL OR organization_id=$3)
		ORDER BY invoice_date ASC, created_at ASC`, invTable, contactCol), contactID, tenantID, orgArg)
	if err != nil {
		h.log.Error("partner ledger: open docs", "error", err)
		response.InternalError(c, "Failed to load partner ledger")
		return
	}
	for drows.Next() {
		var id, number, status string
		var date time.Time
		var total, paid, due float64
		if drows.Scan(&id, &number, &date, &total, &paid, &due, &status) == nil {
			openDocs = append(openDocs, gin.H{
				"id": id, "invoice_number": number, "invoice_date": date,
				"total_amount": total, "amount_paid": paid, "due": due, "status": status,
			})
			totalDue += due
		}
	}
	drows.Close()

	// Payments with their unallocated remainder.
	payments := make([]gin.H, 0)
	var creditAvailable float64
	prows, err := h.db.Query(`
		SELECT p.id::text, COALESCE(p.payment_number,''), p.payment_date, p.amount,
		       p.amount - COALESCE(al.alloc,0) AS unallocated
		FROM payments p
		LEFT JOIN (SELECT payment_id, SUM(amount) AS alloc FROM payment_allocations GROUP BY payment_id) al ON al.payment_id=p.id
		WHERE p.contact_id=$1 AND p.tenant_id=$2 AND p.type=$3 AND p.status IN ('confirmed','posted')
		  AND ($4::uuid IS NULL OR p.organization_id=$4)
		  AND p.amount - COALESCE(al.alloc,0) > 0.001
		ORDER BY p.payment_date ASC, p.created_at ASC`, contactID, tenantID, payType, orgArg)
	if err != nil {
		h.log.Error("partner ledger: payments", "error", err)
		response.InternalError(c, "Failed to load partner ledger")
		return
	}
	for prows.Next() {
		var id, number string
		var date time.Time
		var amount, unalloc float64
		if prows.Scan(&id, &number, &date, &amount, &unalloc) == nil {
			payments = append(payments, gin.H{
				"id": id, "payment_number": number, "payment_date": date,
				"amount": amount, "unallocated": unalloc,
			})
			creditAvailable += unalloc
		}
	}
	prows.Close()

	response.Success(c, gin.H{
		"open_docs":        openDocs,
		"total_due":        totalDue,
		"credit_available": creditAvailable,
		"payments":         payments,
	})
}

// ReconcilePartnerCredit applies a partner's existing unallocated payment credit
// to one of their open invoices/bills — pure matching, no new GL is posted (the
// cash and AR/AP legs were booked when each payment was registered). It consumes
// the partner's unallocated payments oldest-first.
// Body: { contact_id, direction, document_id, amount }
func (h *Handler) ReconcilePartnerCredit(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	orgID, _ := middleware.GetOrganizationID(c)
	var orgArg interface{}
	if orgID != uuid.Nil {
		orgArg = orgID
	}
	var input struct {
		ContactID  string  `json:"contact_id" binding:"required"`
		Direction  string  `json:"direction" binding:"required,oneof=customer vendor"`
		DocumentID string  `json:"document_id" binding:"required"`
		Amount     float64 `json:"amount"` // optional; 0 = settle as much as possible
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}
	contactID, err := uuid.Parse(input.ContactID)
	if err != nil {
		response.BadRequest(c, "Invalid contact_id")
		return
	}
	docID, err := uuid.Parse(input.DocumentID)
	if err != nil {
		response.BadRequest(c, "Invalid document_id")
		return
	}
	invTable, _, payType, docType := partnerDocConfig(input.Direction)
	now := time.Now()

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to reconcile")
		return
	}
	defer tx.Rollback()

	// Document due.
	var docTotal, docPaid float64
	if err := tx.QueryRow(fmt.Sprintf(`SELECT total_amount, COALESCE(amount_paid,0)
		FROM %s WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL`, invTable), docID, tenantID).Scan(&docTotal, &docPaid); err != nil {
		response.BadRequest(c, "Document not found")
		return
	}
	docDue := docTotal - docPaid
	if docDue <= 0.001 {
		response.BadRequest(c, "Document is already fully paid")
		return
	}
	target := docDue
	if input.Amount > 0.001 && input.Amount < target {
		target = input.Amount
	}

	// Partner payments with an unallocated remainder, oldest first.
	type credLine struct {
		id     uuid.UUID
		unallo float64
	}
	var creds []credLine
	rows, err := tx.Query(`
		SELECT p.id, p.amount - COALESCE(al.alloc,0) AS unallocated
		FROM payments p
		LEFT JOIN (SELECT payment_id, SUM(amount) AS alloc FROM payment_allocations GROUP BY payment_id) al ON al.payment_id=p.id
		WHERE p.contact_id=$1 AND p.tenant_id=$2 AND p.type=$3 AND p.status IN ('confirmed','posted')
		  AND ($4::uuid IS NULL OR p.organization_id=$4)
		  AND p.amount - COALESCE(al.alloc,0) > 0.001
		ORDER BY p.payment_date ASC, p.created_at ASC`, contactID, tenantID, payType, orgArg)
	if err != nil {
		h.log.Error("reconcile: load credit", "error", err)
		response.InternalError(c, "Failed to reconcile")
		return
	}
	for rows.Next() {
		var cl credLine
		if rows.Scan(&cl.id, &cl.unallo) == nil {
			creds = append(creds, cl)
		}
	}
	rows.Close()

	applied := 0.0
	for _, cl := range creds {
		if target-applied <= 0.001 {
			break
		}
		take := cl.unallo
		if take > target-applied {
			take = target - applied
		}
		if _, err := tx.Exec(`INSERT INTO payment_allocations (id, payment_id, document_type, document_id, amount, created_at)
			VALUES ($1,$2,$3,$4,$5,$6)`, uuid.New(), cl.id, docType, docID, take, now); err != nil {
			h.log.Error("reconcile: allocation", "error", err)
			response.InternalError(c, "Failed to reconcile")
			return
		}
		applied += take
	}

	if applied <= 0.001 {
		response.BadRequest(c, "No available credit to reconcile")
		return
	}

	if _, err := tx.Exec(fmt.Sprintf(`UPDATE %s
		SET amount_paid = COALESCE(amount_paid,0) + $1,
		    status = CASE WHEN COALESCE(amount_paid,0) + $1 >= total_amount - 0.001 THEN 'paid' ELSE 'partial' END,
		    updated_at = $2
		WHERE id = $3 AND tenant_id = $4`, invTable), applied, now, docID, tenantID); err != nil {
		h.log.Error("reconcile: update doc", "error", err)
		response.InternalError(c, "Failed to reconcile")
		return
	}

	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to reconcile")
		return
	}
	response.Success(c, gin.H{
		"applied":       applied,
		"due_remaining": docDue - applied,
		"fully_paid":    docDue-applied <= 0.001,
	})
}

// ReverseJournalEntry godoc
// @Summary Reverse a journal entry
// @Description Create a reversal entry for a posted journal entry
// @Tags Finance - Journal Entries
// @Accept json
// @Produce json
// @Param id path string true "Journal Entry ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/journal-entries/{id}/reverse [post]
