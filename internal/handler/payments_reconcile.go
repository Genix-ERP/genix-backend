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
	"strings"
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
		ContactID    string  `json:"contact_id" binding:"required"`
		Amount       float64 `json:"amount" binding:"required,gt=0"`
		Direction    string  `json:"direction" binding:"required,oneof=customer vendor"`
		PaymentDate  string  `json:"payment_date,omitempty"`
		Method       string  `json:"method,omitempty"` // cash | bank
		Notes        string  `json:"notes,omitempty"`
		CurrencyID   string  `json:"currency_id,omitempty"`
		ExchangeRate float64 `json:"exchange_rate,omitempty"`
		JournalID    string  `json:"journal_id,omitempty"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Normalised exactly as CreatePayment does, so the two write paths cannot
	// record the same economic event differently. currency_id was hardcoded
	// NULL and exchange_rate 1.0 here, so a USD payment was stored as if it
	// were base currency and then disagreed with POST /payments.
	exchangeRate := input.ExchangeRate
	if exchangeRate <= 0 {
		exchangeRate = 1.0
	}
	var currencyIDPtr *uuid.UUID
	if input.CurrencyID != "" {
		if parsed, perr := uuid.Parse(input.CurrencyID); perr == nil && parsed != uuid.Nil {
			currencyIDPtr = &parsed
		}
	}

	// The GL is kept in base currency, so the journal lines and account balances
	// take the CONVERTED amount while payments.amount keeps what the partner
	// actually handed over. Same convention and same direction as ConfirmPayment
	// and purchase_invoices.go: base = amount * rate. Rate 1 (the ordinary
	// same-currency case) leaves this untouched.
	baseAmount := input.Amount * exchangeRate
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
	var nextNumber int

	// The web modal makes Jurnal required and blocks submit without it, then
	// this path threw the choice away — a tenant with two bank journals could
	// not direct the payment. Prefer the caller's journal when it resolves to a
	// live journal OF THIS TENANT; the tenant predicate is what stops the field
	// becoming a cross-tenant write primitive, so it must not be dropped.
	if input.JournalID != "" {
		if parsed, perr := uuid.Parse(input.JournalID); perr == nil {
			_ = tx.QueryRow(`SELECT id, number_prefix, COALESCE(next_number, 1) FROM journals
				WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL`,
				parsed, tenantID).Scan(&journalID, &prefix, &nextNumber)
		}
	}
	if journalID == uuid.Nil {
		_ = tx.QueryRow(`SELECT id, number_prefix, COALESCE(next_number, 1) FROM journals
			WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2 OR organization_id IS NULL)
			ORDER BY CASE WHEN LOWER(COALESCE(type,''))='cash' THEN 0 WHEN code='GENERAL' THEN 1 ELSE 2 END LIMIT 1`,
			tenantID, orgArg).Scan(&journalID, &prefix, &nextNumber)
	}
	if journalID == uuid.Nil {
		response.BadRequest(c, "No journal is configured for this company.")
		return
	}
	px := ""
	if prefix.Valid {
		px = prefix.String
	}
	// Entry numbers are unique per (tenant, organization) across ALL journals
	// (journal_entries_tenant_org_entry_number_key), so the next number must be
	// computed at that scope. The previous per-journal MAX collided with
	// entries in sibling journals sharing the (typically empty) prefix and the
	// INSERT below 500'd — nextEntryNumberSeq carries the correct scope.
	entryNumber := fmt.Sprintf("%s%06d", px, nextEntryNumberSeq(tx, tenantID, orgPtr, px, nextNumber))
	pType := "receipt"
	if !isCustomer {
		pType = "payment"
	}
	desc := fmt.Sprintf("%s payment", input.Direction)

	// The payment id is generated before the JE so the entry can carry it as
	// source_id, matching ConfirmPayment ('payment_receipt'/'payment' + the
	// PAYMENT id). This used to store the contact id, which broke JE→payment
	// tracing and tripped the DUPLICATE_SOURCE diagnostic on a partner's
	// second registered payment.
	paymentID := uuid.New()
	sourceType := "payment"
	if isCustomer {
		sourceType = "payment_receipt"
	}

	// Journal entry header.
	jeID := uuid.New()
	if _, err := tx.Exec(`INSERT INTO journal_entries
		(id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description, source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$14,$9,$13,$10,$10,'posted',$11,$12,$12)`,
		jeID, tenantID, orgArg, journalID, entryNumber, now, nullIfEmpty(input.Notes), desc, paymentID.String(), baseAmount, userID, now, exchangeRate, sourceType); err != nil {
		h.log.Error("register payment: JE header", "error", err)
		response.InternalError(c, "Failed to post payment")
		return
	}
	jelInsert := func(acct uuid.UUID, contact interface{}, line int, debit, credit float64) error {
		_, e := tx.Exec(`INSERT INTO journal_entry_lines
			(id, journal_entry_id, line_number, account_id, contact_id, description, debit_amount, credit_amount, exchange_rate, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$10,$9)`, uuid.New(), jeID, line, acct, contact, desc, debit, credit, now, exchangeRate)
		return e
	}
	var e1, e2 error
	if isCustomer {
		e1 = jelInsert(cashAcct, nil, 1, baseAmount, 0)          // DR Cash
		e2 = jelInsert(partnerAcct, contactID, 2, 0, baseAmount) // CR AR (partner)
	} else {
		e1 = jelInsert(partnerAcct, contactID, 1, baseAmount, 0) // DR AP (partner)
		e2 = jelInsert(cashAcct, nil, 2, 0, baseAmount)          // CR Cash
	}
	if e1 != nil || e2 != nil {
		h.log.Error("register payment: JE lines", "e1", e1, "e2", e2)
		response.InternalError(c, "Failed to post payment lines")
		return
	}
	// Account balances (cash debit-normal; AR debit-normal; AP credit-normal).
	if isCustomer {
		tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at=$2 WHERE id=$3`, baseAmount, now, cashAcct)
		tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at=$2 WHERE id=$3`, baseAmount, now, partnerAcct)
	} else {
		tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at=$2 WHERE id=$3`, baseAmount, now, partnerAcct)
		tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at=$2 WHERE id=$3`, baseAmount, now, cashAcct)
	}
	tx.Exec(`UPDATE journals SET next_number = next_number + 1 WHERE id=$1`, journalID)

	// Payment record.
	if _, err := tx.Exec(`INSERT INTO payments
		(id, tenant_id, organization_id, type, payment_number, contact_id, payment_date, amount, currency_id, exchange_rate, reference, notes, status, journal_entry_id, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$14,$15,$9,$10,'confirmed',$11,$12,$13,$13)`,
		paymentID, tenantID, orgArg, pType, "PAY-"+entryNumber, contactID, now, input.Amount,
		nullIfEmpty(input.Notes), nullIfEmpty(input.Notes), jeID, userID, now, currencyIDPtr, exchangeRate); err != nil {
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
	// Only settle invoices denominated in the SAME currency as the payment.
	//
	// The loop below compares input.Amount against total_amount - amount_paid.
	// Those are raw numbers with no currency attached, so allocating a 1,000 USD
	// payment against a 1,000,000 UZS invoice would mark it 0.1% paid — the
	// "compare like-for-like" rule, and the failure is silent because both sides
	// are valid numbers. Non-matching invoices are simply left open and the
	// money stays as credit_remaining, which is recoverable; a wrong allocation
	// is not.
	//
	// IS NOT DISTINCT FROM, not '=': currency_id is nullable on both sides and
	// `NULL = NULL` is NULL, so plain equality would refuse to settle the
	// ordinary case where neither the payment nor the invoice names a currency.
	rows, err := tx.Query(fmt.Sprintf(`SELECT id::text, invoice_number, total_amount - COALESCE(amount_paid,0) AS due
		FROM %s WHERE %s=$1 AND tenant_id=$2 AND deleted_at IS NULL
		  AND status NOT IN ('draft','cancelled') AND (total_amount - COALESCE(amount_paid,0)) > 0.001
		  AND currency_id IS NOT DISTINCT FROM $3
		ORDER BY invoice_date ASC, created_at ASC`, table, contactCol), contactID, tenantID, currencyIDPtr)
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
// partnerBalancesQuery builds the filtered CTE that the list, its count and
// the summary all run over.
//
// One builder rather than three copies: the summary must narrow with exactly
// the same filters as the rows, or the cards report totals for a set the user
// is not looking at. Returns the query without ORDER BY (callers append it),
// its argument list, and a message when the request itself is invalid.
func partnerBalancesQuery(c *gin.Context, tenantID uuid.UUID) (string, []interface{}, string) {
	orgID, _ := middleware.GetOrganizationID(c)
	var orgArg interface{}
	if orgID != uuid.Nil {
		orgArg = orgID
	}

	// direction kontrakti (test_30): faqat customer/vendor (bo'sh = customer);
	// noto'g'ri qiymat jimgina customer bo'lib ketmasin — 400.
	direction := c.Query("direction")
	switch direction {
	case "", "customer":
		direction = "customer"
	case "vendor":
	default:
		return "", nil, "direction must be 'customer' or 'vendor'"
	}
	invTable, contactCol, payType, _ := partnerDocConfig(direction)

	// Partner-name search. nil becomes SQL NULL, matching how orgArg is
	// handled above, so the predicate is a no-op when nothing was asked for
	// and there is one query rather than two.
	//
	// c.name only. The web screen searches the same single field
	// (Reconcile.jsx), and widening this to phone or INN here alone would mean
	// the same query returns different partners depending on which app asked.
	// If it should cover more, all three change together.
	//
	// Caller-supplied % and _ stay meaningful as wildcards — matching the
	// other ILIKE searches in this codebase rather than inventing an escaping
	// rule that holds in one endpoint only.
	var searchArg interface{}
	if s := strings.TrimSpace(c.Query("search")); s != "" {
		searchArg = "%" + s + "%"
	}

	// Partners that can actually be reconciled: money owed AND unused credit
	// sitting against the same contact.
	//
	// The 0.001 threshold is fixed by the clients, not chosen here: the mobile
	// card draws its "SOLISHTIRISH MUMKIN" badge at exactly this cutoff. A
	// different threshold on either side means ticking the filter makes a
	// badged row disappear from the list — an inconsistency the user sees
	// directly, on the same screen, in the same moment.
	onlyReconcilable := c.Query("only_reconcilable") == "true"

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
			  AND p.deleted_at IS NULL
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
		  AND ($4::text IS NULL OR c.name ILIKE $4)
		  AND ($5::bool IS NOT TRUE OR (
		        COALESCE(inv.due,0) > 0.001
		        AND GREATEST(COALESCE(pay.ptotal,0)-COALESCE(pay.allocated,0),0) > 0.001
		      ))`, contactCol, invTable, contactCol)

	return q, []interface{}{tenantID, orgArg, payType, searchArg, onlyReconcilable}, ""
}

// partnerBalancesOrder is appended for the row list only. It is deliberately
// not part of partnerBalancesQuery: COUNT and SUM do not need it, and a
// subquery carrying an ORDER BY invites someone to assume the aggregate
// respects it.
//
// c.id is the tiebreaker, and it is not cosmetic. Ordering by (due, name)
// alone is not a total order: two contacts with the same due and the same name
// — duplicate contacts do occur in real books — have an order Postgres is free
// to choose, and it may choose differently for the LIMIT/OFFSET of page 1 than
// for page 2. One partner then appears on both pages and another appears on
// neither. Nothing errors and nothing is logged; a row simply goes missing.
const partnerBalancesOrder = `
		ORDER BY COALESCE(inv.due,0) DESC, name ASC, c.id ASC`

func (h *Handler) GetPartnerBalances(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	q, args, errMsg := partnerBalancesQuery(c, tenantID)
	if errMsg != "" {
		response.BadRequest(c, errMsg)
		return
	}

	// Opt-in paging. One row per contact that has ever been invoiced or paid,
	// so this grows with the customer/vendor book and never shrinks.
	//
	// Opt-in is a contract, not a default worth revisiting: the web screen
	// calls this with no page parameters and expects a bare array. Making
	// paging mandatory would silently truncate it to the first page.
	paginate, page, pageSize, offset := optPagination(c)
	pagedQ := q + partnerBalancesOrder
	pagedArgs := args
	if paginate {
		pagedQ += fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
		pagedArgs = append(append([]interface{}{}, args...), pageSize, offset)
	}

	rows, err := h.db.Query(pagedQ, pagedArgs...)
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
	if !paginate {
		response.Success(c, out)
		return
	}

	total := 0
	// Counting over the CTE query itself keeps the count and the page in exact
	// agreement — including the search and only_reconcilable filters, which are
	// inside q. A count that ignored them would leave has_next permanently
	// true and a paginating client scrolling through empty pages forever.
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM (`+q+`) sub`, args...).Scan(&total); err != nil {
		h.log.Error("Failed to count partner balances", "error", err)
		total = len(out)
	}
	response.Paginated(c, out, page, pageSize, total)
}

// GetPartnerBalancesSummary godoc
// @Summary Totals for the Solishtirish summary cards
// @Tags Payments
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /payments/partner-balances/summary [get]
//
// Accepts the same direction/search/only_reconcilable filters as the list, and
// must: the cards sit above the rows, so a summary that ignored the search
// would report one set of totals over a different set of rows. A paginating
// client cannot compute these itself without summing only the pages it has
// loaded — which is what the mobile app was doing, producing totals that grew
// as the user scrolled.
func (h *Handler) GetPartnerBalancesSummary(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	q, args, errMsg := partnerBalancesQuery(c, tenantID)
	if errMsg != "" {
		response.BadRequest(c, errMsg)
		return
	}

	// The sub-select names its columns positionally, so the aggregate refers to
	// them by the aliases the inner SELECT assigns.
	var totalDue, totalCredit float64
	var reconcilableCount, partnerCount int
	err := h.db.QueryRow(`
		SELECT COALESCE(SUM(due),0),
		       COALESCE(SUM(credit),0),
		       COUNT(*) FILTER (WHERE due > 0.001 AND credit > 0.001),
		       COUNT(*)
		FROM (`+q+`) sub(contact_id, name, invoiced, paid, due, credit)`, args...).
		Scan(&totalDue, &totalCredit, &reconcilableCount, &partnerCount)
	if err != nil {
		h.log.Error("Failed to summarise partner balances", "error", err)
		response.InternalError(c, "Failed to summarise partner balances")
		return
	}

	response.Success(c, gin.H{
		"total_due":          totalDue,
		"total_credit":       totalCredit,
		"reconcilable_count": reconcilableCount,
		"partner_count":      partnerCount,
	})
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
	// direction kontrakti (test_30): faqat customer/vendor (bo'sh = customer);
	// noto'g'ri qiymat jimgina customer bo'lib ketmasin — 400.
	direction := c.Query("direction")
	switch direction {
	case "", "customer":
		direction = "customer"
	case "vendor":
	default:
		response.BadRequest(c, "direction must be 'customer' or 'vendor'")
		return
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
		  AND p.deleted_at IS NULL
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
// the partner's unallocated payments oldest-first, same-currency only, and only
// against an open (not draft/cancelled) document of the caller's org. The
// document row and the candidate payment rows are locked FOR UPDATE so
// concurrent reconciles cannot double-spend credit or overpay the document.
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

	// Document due — locked FOR UPDATE for the length of the tx, so two
	// reconciles against the same document serialise instead of both reading
	// the same due and jointly overpaying it. Status, org and currency are
	// part of the contract, not cosmetics: the UI only offers open documents
	// of the caller's org, but the endpoint accepted ANY tenant document —
	// a draft or cancelled invoice could be flipped straight to 'paid'
	// (live-reproduced 2026-08-13).
	var docTotal, docPaid float64
	var docCurrency sql.NullString
	if err := tx.QueryRow(fmt.Sprintf(`SELECT total_amount, COALESCE(amount_paid,0), currency_id::text
		FROM %s WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL
		  AND status NOT IN ('draft','cancelled')
		  AND ($3::uuid IS NULL OR organization_id=$3)
		FOR UPDATE`, invTable), docID, tenantID, orgArg).Scan(&docTotal, &docPaid, &docCurrency); err != nil {
		response.BadRequest(c, "Document not found or not open for reconciliation")
		return
	}
	var docCurrencyArg interface{}
	if docCurrency.Valid && docCurrency.String != "" {
		docCurrencyArg = docCurrency.String
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

	// Partner payments, oldest first, locked FOR UPDATE. Reconcile is the only
	// path that consumes EXISTING confirmed payments' credit, so locking the
	// payment rows serialises concurrent reconciles for the same partner; the
	// allocation sums are then computed in a SEPARATE statement, whose fresh
	// READ COMMITTED snapshot sees everything the lock-holder committed. One
	// joined query would not: the join's aggregate keeps the pre-wait snapshot
	// even after the row lock is finally granted.
	//
	// Same-currency only (IS NOT DISTINCT FROM — both sides nullable), for the
	// same reason as the FIFO in RegisterPartnerPayment: due and credit are
	// raw numbers, and applying a 100 USD payment against a UZS invoice would
	// record it as 100 so'm paid. Foreign-currency credit simply stays put.
	type credLine struct {
		id     uuid.UUID
		amount float64
	}
	var creds []credLine
	rows, err := tx.Query(`
		SELECT p.id, p.amount
		FROM payments p
		WHERE p.contact_id=$1 AND p.tenant_id=$2 AND p.type=$3 AND p.status IN ('confirmed','posted')
		  AND p.deleted_at IS NULL
		  AND ($4::uuid IS NULL OR p.organization_id=$4)
		  AND p.currency_id IS NOT DISTINCT FROM $5::uuid
		ORDER BY p.payment_date ASC, p.created_at ASC
		FOR UPDATE`, contactID, tenantID, payType, orgArg, docCurrencyArg)
	if err != nil {
		h.log.Error("reconcile: load credit", "error", err)
		response.InternalError(c, "Failed to reconcile")
		return
	}
	for rows.Next() {
		var cl credLine
		if rows.Scan(&cl.id, &cl.amount) == nil {
			creds = append(creds, cl)
		}
	}
	rows.Close()

	applied := 0.0
	for _, cl := range creds {
		if target-applied <= 0.001 {
			break
		}
		// Fresh statement → fresh snapshot: sees allocations committed by any
		// reconcile we just waited on at the FOR UPDATE above.
		var alloc float64
		if err := tx.QueryRow(`SELECT COALESCE(SUM(amount),0) FROM payment_allocations WHERE payment_id=$1`,
			cl.id).Scan(&alloc); err != nil {
			h.log.Error("reconcile: allocation sum", "error", err)
			response.InternalError(c, "Failed to reconcile")
			return
		}
		unallo := cl.amount - alloc
		if unallo <= 0.001 {
			continue
		}
		take := unallo
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
		response.BadRequest(c, "No available credit to reconcile (payments must match the document's currency)")
		return
	}

	// The cap in the WHERE is belt-and-braces under the FOR UPDATE above —
	// same incremental-claim pattern as the sales-side RecordPayment: if the
	// arithmetic ever disagrees with the row, the update matches zero rows and
	// the whole tx (allocations included) rolls back instead of overpaying.
	updRes, err := tx.Exec(fmt.Sprintf(`UPDATE %s
		SET amount_paid = COALESCE(amount_paid,0) + $1,
		    status = CASE WHEN COALESCE(amount_paid,0) + $1 >= total_amount - 0.001 THEN 'paid' ELSE 'partial' END,
		    updated_at = $2
		WHERE id = $3 AND tenant_id = $4
		  AND COALESCE(amount_paid,0) + $1 <= total_amount + 0.001`, invTable), applied, now, docID, tenantID)
	if err != nil {
		h.log.Error("reconcile: update doc", "error", err)
		response.InternalError(c, "Failed to reconcile")
		return
	}
	if n, _ := updRes.RowsAffected(); n == 0 {
		response.BadRequest(c, "OVER_PAYMENT: credit exceeds the document's remaining balance")
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
