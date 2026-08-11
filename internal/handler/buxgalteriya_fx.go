package handler

// File: buxgalteriya_fx.go
//
// TT Buxgalteriya ERP §4.6 — FX gain/loss auto-posting.
//
// When a journal entry mixes exchange rates across its lines, the base-currency
// (UZS) totals computed from amount_base = gross * exchange_rate may not tie,
// even though the operation-currency Dt/Kt does tie. The difference is a realized
// FX gain (Kt > Dt in base) or FX loss (Dt > Kt in base) that we must book
// automatically to 9540 or 9630 respectively.
//
// This helper is called from within the same DB transaction that created the
// user's journal lines, BEFORE the transaction is committed. It returns the
// appended line's amount so the caller can update total_debit/total_credit
// on the header. If the base-currency totals already balance (within 0.01 UZS),
// it returns zero and adds nothing.

import (
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
)

// fxAdjustResult describes what was appended (if anything) to the entry.
type fxAdjustResult struct {
	AppendedLine bool
	DebitAdded   float64 // added to total_debit on header
	CreditAdded  float64 // added to total_credit on header
	BaseDelta    float64 // |sum(Dt base) - sum(Kt base)|, positive
	FXAccountID  uuid.UUID
	IsGain       bool // true = credit to 9540, false = debit to 9630
}

// appendFXBalancingLine computes the base-currency imbalance of the entry's
// already-inserted lines and, if non-trivial, inserts a balancing line to 9540
// (gain) or 9630 (loss).
//
// Arguments:
//
//	tx           — the open transaction (same one the caller is about to commit)
//	tenantID     — tenant scope for account lookup
//	orgID        — organization to scope the FX accounts (nullable)
//	entryID      — journal_entries.id the caller just created
//	nextLineNum  — line_number to use (caller's lines are 1..N, this is N+1)
//	now          — timestamp for created_at
//
// Returns (result, error). result.AppendedLine is false when no adjustment was
// needed. The caller should update total_debit/total_credit on the header using
// result.DebitAdded / result.CreditAdded.
func (h *Handler) appendFXBalancingLine(
	tx *sql.Tx,
	tenantID uuid.UUID,
	orgID *uuid.UUID,
	entryID uuid.UUID,
	nextLineNum int,
	now time.Time,
) (fxAdjustResult, error) {

	var res fxAdjustResult

	// 1. Sum base-currency totals from the lines we just inserted.
	var baseDebit, baseCredit float64
	err := tx.QueryRow(`
		SELECT COALESCE(SUM(CASE WHEN debit_amount  > 0 THEN amount_base ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN credit_amount > 0 THEN amount_base ELSE 0 END), 0)
		FROM journal_entry_lines
		WHERE journal_entry_id = $1
	`, entryID).Scan(&baseDebit, &baseCredit)
	if err != nil {
		return res, fmt.Errorf("fx balance sum: %w", err)
	}

	delta := baseDebit - baseCredit
	// Ignore anything under 1 tiyin — rounding noise, not a real FX difference.
	if math.Abs(delta) < 0.01 {
		return res, nil
	}

	// 2. Decide gain vs loss.
	//    delta > 0 (Dt > Kt)  → we need more Kt → book FX loss: Dt 9630 / Kt <implicit balancing>
	//      BUT that would add to Dt again. Correct interpretation:
	//      delta > 0 means the debit side is "heavier" in base currency. Our balancing
	//      entry should CREDIT the same amount so base totals tie, so we credit 9540 (gain).
	//    delta < 0 (Kt > Dt)  → credit side is heavier → we DEBIT 9630 (loss).
	//
	// Rationale: if we paid 1000 USD at rate 12500 but accrued the payable at 12300,
	// Dt payable (in base) = 12,300,000 and Kt bank (in base) = 12,500,000, so Kt>Dt,
	// which means we "spent" more UZS than we owed → FX LOSS → Dt 9630 / (implicit Kt reconciled via bank).
	// Here we just need to balance: delta<0 → Dt 9630.
	var fxCode string
	var debitAdd, creditAdd float64
	absDelta := math.Abs(delta)

	if delta > 0 {
		// Dt heavier → credit a gain line
		fxCode = "9540"
		creditAdd = absDelta
		res.IsGain = true
	} else {
		// Kt heavier → debit a loss line
		fxCode = "9630"
		debitAdd = absDelta
		res.IsGain = false
	}

	// 3. Look up the FX account for this tenant/org.
	var fxAccountID uuid.UUID
	lookupQ := `
		SELECT id FROM accounts
		WHERE tenant_id = $1
		  AND code = $2
		  AND deleted_at IS NULL
		  AND is_active = true
	`
	args := []interface{}{tenantID, fxCode}
	if orgID != nil && *orgID != uuid.Nil {
		lookupQ += " AND (organization_id = $3 OR organization_id IS NULL)"
		lookupQ += " ORDER BY (organization_id IS NULL) ASC, created_at ASC"
		args = append(args, *orgID)
	} else {
		lookupQ += " ORDER BY created_at ASC"
	}
	lookupQ += " LIMIT 1"

	if err := tx.QueryRow(lookupQ, args...).Scan(&fxAccountID); err != nil {
		if err == sql.ErrNoRows {
			// Non-fatal: log and leave the entry as-is; the DB trigger will catch
			// any invariant violation. Caller's entry remains usable in practice
			// because Dt=Kt still holds in OPERATION currency.
			h.log.Warn("FX balancing account not found; skipping auto-post",
				"code", fxCode, "tenant", tenantID, "entry", entryID)
			return res, nil
		}
		return res, fmt.Errorf("fx account lookup: %w", err)
	}

	// 4. Insert the balancing line.
	lineID := uuid.New()
	description := fmt.Sprintf("Valyuta kursi farqi (avtomatik, TT §4.6): delta=%.2f UZS", delta)
	_, err = tx.Exec(`
		INSERT INTO journal_entry_lines (
			id, journal_entry_id, line_number, account_id,
			description, debit_amount, credit_amount,
			exchange_rate, amount_base, analytics_json, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 1, $8, '{}'::jsonb, $9)
	`, lineID, entryID, nextLineNum, fxAccountID,
		description, debitAdd, creditAdd, absDelta, now)
	if err != nil {
		return res, fmt.Errorf("fx line insert: %w", err)
	}

	res.AppendedLine = true
	res.DebitAdded = debitAdd
	res.CreditAdded = creditAdd
	res.BaseDelta = absDelta
	res.FXAccountID = fxAccountID
	return res, nil
}
