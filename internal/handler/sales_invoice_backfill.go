package handler

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Backfilling ledger entries for invoices that never got one.
//
// CreateInvoiceFromOrder auto-posts a journal entry on creation — but until
// the chart self-heal (ensureDefaultChart), the whole GL block was skipped
// SILENTLY for a tenant with no SALES journal or no 4010. Every invoice such
// a tenant issued came out status 'sent' with journal_entry_id NULL: real
// receivables in the invoice list, nothing in the ledger. The finance
// dashboard reads the ledger, so it showed Daromad 0 against a screen full
// of unpaid invoices — the mismatch in the 2026-08-11 screenshot.
//
// The self-heal fixes new invoices. This fixes the ones already issued: a
// bounded, idempotent repair that posts the missing issuance entry (Debit AR,
// Credit income/tax) for each non-draft invoice with no journal_entry_id.
// It runs lazily from GetFinanceDashboard — the same read-side-repair pattern
// ListAccounts uses to seed the chart — so the person who opens the dashboard
// and would see the wrong zero is the one whose visit repairs it.
//
// Deliberate simplifications, matching what can be known after the fact:
//
//   - No COGS leg. Historical cost would have to be reconstructed from
//     today's cost_price, which is wrong for past periods; revenue and AR are
//     knowable exactly, cost is not. Future invoices get full COGS posting
//     from the normal path.
//   - If the tax account (6420) is missing, tax folds into revenue rather
//     than dropping a credit line — the entry must balance.
//   - Rows in locked periods are skipped, not forced.
//
// Each invoice posts in its OWN transaction with the row locked and the
// journal_entry_id re-checked under the lock, so a concurrent dashboard load
// cannot double-post, and one bad row cannot poison the rest.

// countUnpostedSalesInvoices is the cheap guard the dashboard runs every
// load; for a healthy tenant it is one indexed count returning 0.
func (h *Handler) countUnpostedSalesInvoices(tenantID uuid.UUID, orgID *uuid.UUID) int {
	q := `SELECT COUNT(*) FROM sales_invoices
	      WHERE tenant_id = $1 AND deleted_at IS NULL
	        AND journal_entry_id IS NULL
	        AND status NOT IN ('draft', 'cancelled', 'void')
	        AND total_amount > 0`
	args := []interface{}{tenantID}
	if orgID != nil {
		q += ` AND organization_id = $2`
		args = append(args, *orgID)
	}
	var n int
	_ = h.db.QueryRow(q, args...).Scan(&n)
	return n
}

// backfillSalesInvoiceJournalEntries posts the missing issuance entries for
// up to `limit` invoices. Returns how many posted and how many were skipped
// (locked period, unresolvable accounts, or lost the row race).
func (h *Handler) backfillSalesInvoiceJournalEntries(tenantID uuid.UUID, orgID *uuid.UUID, limit int) (posted, skipped int) {
	listQ := `SELECT id FROM sales_invoices
	          WHERE tenant_id = $1 AND deleted_at IS NULL
	            AND journal_entry_id IS NULL
	            AND status NOT IN ('draft', 'cancelled', 'void')
	            AND total_amount > 0`
	args := []interface{}{tenantID}
	if orgID != nil {
		listQ += ` AND organization_id = $2`
		args = append(args, *orgID)
	}
	listQ += fmt.Sprintf(` ORDER BY invoice_date ASC, created_at ASC LIMIT %d`, limit)

	// The invoices exist precisely because the chart didn't; make sure it does
	// before trying to post against it.
	if findAccount(h.db, tenantID, orgID, "accounts receivable", "4010") == uuid.Nil {
		if !h.ensureDefaultChart(tenantID, orgID) {
			return 0, 0
		}
	}

	rows, err := h.db.Query(listQ, args...)
	if err != nil {
		h.log.Error("invoice backfill: list failed", "error", err)
		return 0, 0
	}
	ids := make([]uuid.UUID, 0, limit)
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if len(ids) == 0 {
		return 0, 0
	}

	for _, id := range ids {
		if h.backfillOneInvoice(tenantID, id) {
			posted++
		} else {
			skipped++
		}
	}
	if posted > 0 || skipped > 0 {
		h.log.Info("invoice backfill: posted missing journal entries",
			"tenant_id", tenantID, "posted", posted, "skipped", skipped)
	}
	return posted, skipped
}

func (h *Handler) backfillOneInvoice(tenantID, invoiceID uuid.UUID) bool {
	tx, err := h.db.Begin()
	if err != nil {
		return false
	}
	defer tx.Rollback()

	var (
		invOrgID      *uuid.UUID
		customerID    uuid.UUID
		invoiceNumber string
		invoiceDate   time.Time
		subtotal      float64
		taxAmount     float64
		totalAmount   float64
		jeID          *uuid.UUID
		status        string
	)
	// Re-check everything under the row lock — the list query ran unlocked.
	err = tx.QueryRow(`
		SELECT organization_id, customer_id, invoice_number, invoice_date,
		       COALESCE(subtotal, 0), COALESCE(tax_amount, 0), COALESCE(total_amount, 0),
		       journal_entry_id, status
		FROM sales_invoices
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
		FOR UPDATE`, invoiceID, tenantID,
	).Scan(&invOrgID, &customerID, &invoiceNumber, &invoiceDate,
		&subtotal, &taxAmount, &totalAmount, &jeID, &status)
	if err != nil || jeID != nil || totalAmount <= 0 ||
		status == "draft" || status == "cancelled" || status == "void" {
		return false
	}

	// Same period rules as posting anything else.
	if msg := h.checkLockDate(tenantID, invoiceDate); msg != "" {
		return false
	}
	if msg := h.checkPeriodLock(tenantID, invoiceDate); msg != "" {
		return false
	}

	var salesJournalID uuid.UUID
	var numberPrefix sql.NullString
	if err := tx.QueryRow(`
		SELECT id, number_prefix
		FROM journals WHERE tenant_id = $1 AND code IN ('SALES', 'SAL') AND deleted_at IS NULL`,
		tenantID,
	).Scan(&salesJournalID, &numberPrefix); err != nil {
		return false
	}

	arAccountID := getContactDefaultAccount(tx, customerID, "receivable", invOrgID)
	if arAccountID == uuid.Nil {
		arAccountID = findAccount(tx, tenantID, invOrgID, "accounts receivable", "4010")
	}
	if arAccountID == uuid.Nil {
		return false
	}
	taxAccountID := findAccount(tx, tenantID, invOrgID, "QQS bo'yicha qarz", "6420")
	fallbackRevenue := findAccount(tx, tenantID, invOrgID, "sales revenue", "9010")

	// Income per product category, like the live posting path; anything not
	// attributable to a product line lands on the 9010 fallback.
	revenueGrouped := make(map[uuid.UUID]float64)
	var groupedTotal float64
	lineRows, lineErr := tx.Query(`
		SELECT sil.product_id, COALESCE(sil.line_total, 0)
		FROM sales_invoice_lines sil
		WHERE sil.sales_invoice_id = $1 AND sil.product_id IS NOT NULL`, invoiceID)
	if lineErr == nil {
		type pl struct {
			productID uuid.UUID
			total     float64
		}
		var pls []pl
		for lineRows.Next() {
			var p pl
			if lineRows.Scan(&p.productID, &p.total) == nil && p.total > 0 {
				pls = append(pls, p)
			}
		}
		lineRows.Close()
		for _, p := range pls {
			acct := getCategoryAccounts(tx, tenantID, invOrgID, p.productID).IncomeAccountID
			if acct == uuid.Nil {
				acct = fallbackRevenue
			}
			if acct != uuid.Nil {
				revenueGrouped[acct] += p.total
				groupedTotal += p.total
			}
		}
	}
	// Remainder (service lines, rounding, or no product lines at all).
	if rem := subtotal - groupedTotal; rem > 0.005 {
		if fallbackRevenue == uuid.Nil {
			return false
		}
		revenueGrouped[fallbackRevenue] += rem
	}
	// Tax with nowhere to go folds into revenue so the entry balances.
	creditTax := taxAmount
	if creditTax > 0 && taxAccountID == uuid.Nil {
		if fallbackRevenue == uuid.Nil {
			return false
		}
		revenueGrouped[fallbackRevenue] += creditTax
		creditTax = 0
	}

	now := time.Now()
	prefix := ""
	if numberPrefix.Valid {
		prefix = numberPrefix.String
	}
	nextNumber := nextEntryNumberSeq(tx, tenantID, invOrgID, prefix, 1)
	entryNumber := fmt.Sprintf("%s%06d", prefix, nextNumber)

	journalEntryID := uuid.New()
	description := fmt.Sprintf("Sales Invoice %s (backfilled GL entry)", invoiceNumber)
	if _, err := tx.Exec(`
		INSERT INTO journal_entries (
			id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
			source_type, source_id, exchange_rate, total_debit, total_credit, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'sales_invoice', $9, 1.0, $10, $10, 'posted', $11, $11)`,
		journalEntryID, tenantID, invOrgID, salesJournalID, entryNumber, invoiceDate,
		invoiceNumber, description, invoiceID, totalAmount, now,
	); err != nil {
		h.log.Error("invoice backfill: entry insert failed", "error", err, "invoice_id", invoiceID)
		return false
	}

	lineNumber := 1
	if _, err := tx.Exec(`
		INSERT INTO journal_entry_lines (
			id, journal_entry_id, line_number, account_id, contact_id, description,
			debit_amount, credit_amount, exchange_rate, created_at
		) VALUES ($1, $2, $3, $4, $5, 'Accounts Receivable', $6, 0, 1.0, $7)`,
		uuid.New(), journalEntryID, lineNumber, arAccountID, customerID, totalAmount, now,
	); err != nil {
		return false
	}
	// Balance updates mirror SendInvoice, so backfilled and live postings move
	// current_balance identically.
	if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`,
		totalAmount, now, arAccountID); err != nil {
		return false
	}
	lineNumber++

	for acct, amount := range revenueGrouped {
		if _, err := tx.Exec(`
			INSERT INTO journal_entry_lines (
				id, journal_entry_id, line_number, account_id, description,
				debit_amount, credit_amount, exchange_rate, created_at
			) VALUES ($1, $2, $3, $4, 'Sales Revenue', 0, $5, 1.0, $6)`,
			uuid.New(), journalEntryID, lineNumber, acct, amount, now,
		); err != nil {
			return false
		}
		if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`,
			amount, now, acct); err != nil {
			return false
		}
		lineNumber++
	}

	if creditTax > 0 {
		if _, err := tx.Exec(`
			INSERT INTO journal_entry_lines (
				id, journal_entry_id, line_number, account_id, description,
				debit_amount, credit_amount, exchange_rate, created_at
			) VALUES ($1, $2, $3, $4, 'Tax Payable', 0, $5, 1.0, $6)`,
			uuid.New(), journalEntryID, lineNumber, taxAccountID, creditTax, now,
		); err != nil {
			return false
		}
		if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`,
			creditTax, now, taxAccountID); err != nil {
			return false
		}
	}

	if _, err := tx.Exec(`UPDATE sales_invoices SET journal_entry_id = $1, updated_at = $2 WHERE id = $3`,
		journalEntryID, now, invoiceID); err != nil {
		return false
	}
	if _, err := tx.Exec(`UPDATE journals SET next_number = GREATEST(COALESCE(next_number, 1), $1) WHERE id = $2`,
		nextNumber+1, salesJournalID); err != nil {
		return false
	}

	return tx.Commit() == nil
}
