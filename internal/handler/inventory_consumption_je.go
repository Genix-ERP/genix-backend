package handler

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// inventory_consumption_je.go
//
// PURPOSE
// A single shared helper for posting the "CR inventory / DR cost-of-
// materials" journal entry that every inventory-consumption code path
// in the system OWES whenever it decrements `inventory.quantity_on_hand`
// for a non-internal-transfer reason.
//
// Historical context: prior to this helper, several decrement paths
// (construction YAKUNIY finalisation, work-order BOM consumption, ad-
// hoc work-order materials, POS sales, intercompany ship, sales
// delivery without invoice, production splits, etc.) were updating the
// inventory table and writing module-local cost rows (e.g.
// construction_expense_lines) but never posting the offsetting
// journal entry to the GL. The result was that the inventory GL
// account balance (Buxgalteriya) drifted higher than the physical
// inventory value (Ombor "Jami qiymat") as goods left stock without
// the ledger being told. By mid-2026 the drift was 334M UZS across
// just EVROPLIT (-304M) and LUXURYMEBEL (-31M).
//
// USAGE
// Call after a successful inventory decrement, within the same
// transaction (or via h.db if the caller doesn't use one). Pass a
// stable `idempotencyKey` so re-runs of the same workflow step
// (e.g. a reconfirm) don't double-post.
//
//	h.postInventoryConsumptionJE(tx, postInventoryConsumptionArgs{
//	    TenantID:       tenantID,
//	    OrganizationID: orgIDPtr,
//	    ProductID:      productID,
//	    Quantity:       consumed,
//	    UnitCost:       unitCost,
//	    SourceType:     "construction_yakuniy",
//	    SourceID:       &reservationID,  // optional, helps audit
//	    IdempotencyKey: fmt.Sprintf("CONS-YAK-%d-%s", workID, reservationID),
//	    Description:    "Yakunlangan ish #N — material name",
//	})
//
// On any error (no inventory account configured, no journal available,
// duplicate idempotency key, etc.) the function logs and returns
// silently — the caller's primary work (the decrement and the module-
// local row) MUST not be rolled back just because the JE couldn't be
// posted. The diagnostic admin endpoint
// (`GET /api/v1/admin/inventory-reconcile`) will surface any remaining
// gap so we can catch silent failures.

// dbExecQuerier is the minimum surface this helper needs. Both
// *database.DB (via embedded *sql.DB) and *sql.Tx satisfy it, so
// callers can pass whichever context they're in.
type dbExecQuerier interface {
	QueryRow(query string, args ...interface{}) *sql.Row
	Exec(query string, args ...interface{}) (sql.Result, error)
}

// postInventoryConsumptionArgs is the parameter bag for the helper.
// All fields except SourceID are required; if Quantity*UnitCost <= 0
// the helper is a no-op (nothing to recognize).
type postInventoryConsumptionArgs struct {
	TenantID       uuid.UUID
	OrganizationID *uuid.UUID
	ProductID      uuid.UUID
	Quantity       float64
	UnitCost       float64
	SourceType     string      // e.g. "construction_yakuniy", "production_consume"
	SourceID       interface{} // nullable; *uuid.UUID, *int64, or nil
	IdempotencyKey string      // unique per consumption event; used as JE reference
	Description    string      // human-readable, copied into JE description
}

// postInventoryConsumptionJE posts:
//
//	DR  Cost-of-materials account  (product's expense account, fallback 9110)
//	CR  Inventory account          (product's stock-valuation account, fallback 1010)
//
// for `Quantity × UnitCost` UZS, tagged with `IdempotencyKey` as the
// JE reference so re-runs of the same workflow step (e.g. a reconfirm
// or a retry after a transient failure) don't double-post.
//
// `q` is the SQL context to use — pass *sql.Tx if you're inside a
// transaction, or h.db otherwise. The function is best-effort: any
// failure is logged and swallowed, never propagated, so the caller's
// primary work (decrement + module-local row) is never rolled back
// just because the GL couldn't be touched.
func (h *Handler) postInventoryConsumptionJE(q dbExecQuerier, args postInventoryConsumptionArgs) {
	amount := args.Quantity * args.UnitCost
	if amount <= 0 {
		// Nothing to recognize. Common for free-issue items or for
		// products that have a zero cost on file. Not a bug.
		return
	}

	// Header + both lines MUST share one transaction: the migration-416 balance
	// trigger is deferred to COMMIT, so in autocommit mode the DR-line statement
	// commits alone, fails the trigger, and leaves a posted ZERO-LINE entry (the
	// exact bug class 416 was written to stop). Callers that pass h.db get a
	// private tx here; callers already inside a tx are unaffected.
	// *sql.Tx has no Begin method, so a caller already inside a tx never matches;
	// both *sql.DB and the handler's *database.DB (embedded *sql.DB) do.
	if db, isDB := q.(interface{ Begin() (*sql.Tx, error) }); isDB {
		tx, txErr := db.Begin()
		if txErr != nil {
			h.log.Error("postInventoryConsumptionJE: begin tx failed", "error", txErr)
			return
		}
		defer tx.Rollback()
		h.postInventoryConsumptionJE(tx, args)
		if cErr := tx.Commit(); cErr != nil {
			h.log.Error("postInventoryConsumptionJE: commit failed", "error", cErr, "key", args.IdempotencyKey)
		}
		return
	}

	// ── Idempotency: skip if we've already posted this exact event. ──
	// We tag the JE with the caller-supplied IdempotencyKey as the
	// reference column. Re-runs (e.g. user double-clicks "Confirm
	// YAKUNIY", a transient DB failure causes a retry, or the
	// estimate-sweep loop sees a row the reservation loop already
	// processed) all converge to the same key and skip here.
	//
	// The match is tenant-scoped because non-UUID idempotency-key
	// shapes (e.g. "CONS-YAK-SUB-<int64>-W<int64>") could theoretically
	// collide across tenants. UUID-based keys won't, but scoping is
	// cheap insurance.
	if args.IdempotencyKey != "" {
		var existing int
		_ = q.QueryRow(`
			SELECT COUNT(*) FROM journal_entries
			WHERE reference = $1 AND tenant_id = $2 AND deleted_at IS NULL
		`, args.IdempotencyKey, args.TenantID).Scan(&existing)
		if existing > 0 {
			return
		}
	}

	// ── Resolve the cost-of-materials (DR) and inventory (CR) accounts.
	// getCategoryAccounts walks: product → product_category → category's
	// configured accounts; falls back to tenant-wide name/code lookup
	// for any field left blank (9110 COGS, 1010 inventory). Both
	// returned IDs are guaranteed leaves (resolveLeafAccount inside the
	// helper).
	ca := getCategoryAccounts(q, args.TenantID, args.OrganizationID, args.ProductID)
	// The CREDIT must land on the account the goods were DEBITED to when they
	// arrived. Receipts (goods receipt, PO receive) and sales-invoice COGS all
	// route through getInventoryAccountByType — 2910 trade / 1010 raw / 2810
	// finished — while this path used only the category's valuation account,
	// whose blank-field fallback is 1010. So a traded good was received into
	// 2910 and issued out of 1010: one account climbed forever, the other went
	// negative, and neither matched the stock on hand.
	if invAcct := getInventoryAccountByType(q, args.TenantID, args.OrganizationID, args.ProductID); invAcct != uuid.Nil {
		ca.StockValuationAccountID = invAcct
	}
	if ca.ExpenseAccountID == uuid.Nil || ca.StockValuationAccountID == uuid.Nil {
		// One of the two legs has no account to post to. Log so the
		// admin can configure the missing piece in product_categories
		// or seed the tenant's chart with 9110/1010. The reconcile
		// endpoint will continue to show this org as out-of-balance
		// until configured.
		h.log.Warn("postInventoryConsumptionJE: missing accounts; skipping JE",
			"tenant", args.TenantID,
			"org", args.OrganizationID,
			"product", args.ProductID,
			"expense_acct", ca.ExpenseAccountID,
			"stock_valuation_acct", ca.StockValuationAccountID,
			"source", args.SourceType,
			"idempotency_key", args.IdempotencyKey,
		)
		return
	}

	// ── Pick a journal. Prefer STOCK/MISC/GEN/GENERAL — order matches
	// the rest of the inventory/finance code in this package. Per-tenant
	// scoped. Migration 188 seeds STOCK for every tenant; the fallback
	// list catches older tenants without that seed.
	var journalID uuid.UUID
	var nextNumber int
	if err := q.QueryRow(`
		SELECT id, COALESCE(next_number, 1)
		FROM journals
		WHERE tenant_id = $1
		  AND deleted_at IS NULL
		  AND code IN ('STOCK','MISC','GEN','GENERAL')
		ORDER BY CASE code
		    WHEN 'STOCK'   THEN 1
		    WHEN 'MISC'    THEN 2
		    WHEN 'GEN'     THEN 3
		    WHEN 'GENERAL' THEN 4
		    ELSE 99
		END
		LIMIT 1
	`, args.TenantID).Scan(&journalID, &nextNumber); err != nil {
		h.log.Warn("postInventoryConsumptionJE: no journal available; skipping JE",
			"tenant", args.TenantID,
			"source", args.SourceType,
			"idempotency_key", args.IdempotencyKey,
			"error", err,
		)
		return
	}

	// ── Build and insert the JE. ──
	now := time.Now()
	entryID := uuid.New()
	entryNumber := fmt.Sprintf("CONS%06d", nextNumber)

	// Normalize OrganizationID for the insert. journal_entries.organization_id
	// is nullable; pass nil rather than the zero UUID when the caller
	// didn't supply one.
	var orgArg interface{}
	if args.OrganizationID != nil && *args.OrganizationID != uuid.Nil {
		orgArg = *args.OrganizationID
	}

	if _, err := q.Exec(`
		INSERT INTO journal_entries (
			id, tenant_id, organization_id, journal_id, entry_number,
			entry_date, reference, description,
			source_type, source_id, exchange_rate,
			total_debit, total_credit, status,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6::date, $7, $8,
			$9, $10, 1.0,
			$11, $11, 'posted',
			$6, $6
		)
	`,
		entryID, args.TenantID, orgArg, journalID, entryNumber,
		now, args.IdempotencyKey, args.Description,
		args.SourceType, args.SourceID,
		amount,
	); err != nil {
		h.log.Error("postInventoryConsumptionJE: journal_entry insert failed",
			"error", err,
			"tenant", args.TenantID,
			"source", args.SourceType,
			"idempotency_key", args.IdempotencyKey,
		)
		return
	}

	// Bump the journal sequence so the next CONS / MR / OPEN entry
	// doesn't collide on entry_number.
	if _, err := q.Exec(`
		UPDATE journals SET next_number = next_number + 1, updated_at = $1
		WHERE id = $2
	`, now, journalID); err != nil {
		h.log.Warn("postInventoryConsumptionJE: journal next_number bump failed",
			"error", err, "journal", journalID)
	}

	// ── DR Cost-of-materials (expense). ──
	if _, err := q.Exec(`
		INSERT INTO journal_entry_lines (
			id, journal_entry_id, line_number, account_id,
			description, debit_amount, credit_amount,
			exchange_rate, amount_base, analytics_json, created_at
		) VALUES (
			$1, $2, 1, $3,
			$4, $5, 0,
			1.0, $5, '{}'::jsonb, $6
		)
	`,
		uuid.New(), entryID, ca.ExpenseAccountID,
		"Material consumption — "+args.Description,
		amount, now,
	); err != nil {
		h.log.Error("postInventoryConsumptionJE: DR line insert failed",
			"error", err, "entry", entryID)
		return
	}
	if _, err := q.Exec(`
		UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2
		WHERE id = $3
	`, amount, now, ca.ExpenseAccountID); err != nil {
		h.log.Warn("postInventoryConsumptionJE: DR balance bump failed",
			"error", err, "account", ca.ExpenseAccountID)
	}

	// ── CR Inventory. ──
	if _, err := q.Exec(`
		INSERT INTO journal_entry_lines (
			id, journal_entry_id, line_number, account_id,
			description, debit_amount, credit_amount,
			exchange_rate, amount_base, analytics_json, created_at
		) VALUES (
			$1, $2, 2, $3,
			$4, 0, $5,
			1.0, $5, '{}'::jsonb, $6
		)
	`,
		uuid.New(), entryID, ca.StockValuationAccountID,
		"Inventory issued — "+args.Description,
		amount, now,
	); err != nil {
		h.log.Error("postInventoryConsumptionJE: CR line insert failed",
			"error", err, "entry", entryID)
		return
	}
	if _, err := q.Exec(`
		UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2
		WHERE id = $3
	`, amount, now, ca.StockValuationAccountID); err != nil {
		h.log.Warn("postInventoryConsumptionJE: CR balance bump failed",
			"error", err, "account", ca.StockValuationAccountID)
	}
}
