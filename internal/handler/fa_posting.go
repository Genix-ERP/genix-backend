package handler

import (
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
)

// ============================================================================
// Fixed Assets v1 — journal posting helper.
//
// Balances follow the DEBIT-POSITIVE convention established by migration 407
// (current_balance = Σdebit − Σcredit for every account, regardless of normal
// balance), matching runAutoDepreciation, goods_receipts, inventory, etc. Every
// line therefore applies `current_balance += debit − credit`.
// ============================================================================

// faJELine is one posting line by account CODE (resolved to UUID at write time).
type faJELine struct {
	Code   string
	Debit  float64
	Credit float64
	Desc   string
}

// faGetJournal returns the tenant's asset/general journal id, its next entry
// number, and its prefix. Caller increments next_number after a successful post.
func faGetJournal(tx *sql.Tx, tenantID uuid.UUID) (uuid.UUID, int, string) {
	var journalID uuid.UUID
	var nextNumber int
	var prefix sql.NullString
	_ = tx.QueryRow(`
		SELECT id, COALESCE(next_number, 1), number_prefix
		FROM journals
		WHERE tenant_id = $1 AND code IN ('ASSET','MISC','GENERAL') AND deleted_at IS NULL
		ORDER BY CASE code WHEN 'ASSET' THEN 0 WHEN 'MISC' THEN 1 ELSE 2 END
		LIMIT 1`, tenantID).Scan(&journalID, &nextNumber, &prefix)
	p := "FA"
	if prefix.Valid && prefix.String != "" {
		p = prefix.String
	}
	return journalID, nextNumber, p
}

// faPostJournal writes one balanced, posted journal entry inside tx and applies
// account-balance deltas (debit-positive). Zero-amount lines are dropped. It
// resolves each account code to a leaf account UUID; a missing code or an
// imbalance is a hard error (nothing is left half-written because tx rolls back).
func (h *Handler) faPostJournal(
	tx *sql.Tx, tenantID uuid.UUID, orgID *uuid.UUID, journalID uuid.UUID,
	entryNumber, reference, description, sourceType string, sourceID uuid.UUID,
	date time.Time, lines []faJELine,
) (uuid.UUID, error) {
	if journalID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("no asset journal configured")
	}

	type resolved struct {
		id            uuid.UUID
		debit, credit float64
		desc          string
	}
	rl := make([]resolved, 0, len(lines))
	var totalDebit, totalCredit float64
	for _, l := range lines {
		if l.Debit == 0 && l.Credit == 0 {
			continue
		}
		id, ok := h.resolveAccountID(tenantID, l.Code)
		if !ok {
			return uuid.Nil, fmt.Errorf("account not found or not postable: %s", l.Code)
		}
		rl = append(rl, resolved{id, l.Debit, l.Credit, l.Desc})
		totalDebit += l.Debit
		totalCredit += l.Credit
	}
	if len(rl) == 0 {
		return uuid.Nil, fmt.Errorf("no lines to post")
	}
	if math.Abs(totalDebit-totalCredit) > 0.005 {
		return uuid.Nil, fmt.Errorf("unbalanced entry: debit %.2f != credit %.2f", totalDebit, totalCredit)
	}

	jeID := uuid.New()
	now := time.Now()
	if _, err := tx.Exec(`
		INSERT INTO journal_entries (
			id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
			source_type, source_id, exchange_rate, total_debit, total_credit, status, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,1.0,$11,$12,'posted',$13,$13)`,
		jeID, tenantID, orgID, journalID, entryNumber, date, reference, description,
		sourceType, sourceID, totalDebit, totalCredit, now); err != nil {
		return uuid.Nil, fmt.Errorf("insert journal entry: %w", err)
	}

	for i, r := range rl {
		if _, err := tx.Exec(`
			INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,1.0,$8)`,
			uuid.New(), jeID, i+1, r.id, r.desc, r.debit, r.credit, now); err != nil {
			return uuid.Nil, fmt.Errorf("insert journal line: %w", err)
		}
		// Debit-positive convention (migration 407): balance += debit − credit.
		if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`,
			r.debit-r.credit, now, r.id); err != nil {
			return uuid.Nil, fmt.Errorf("update account balance: %w", err)
		}
	}
	return jeID, nil
}

// faBumpJournalNumber advances the journal's next_number by n after posting.
func faBumpJournalNumber(tx *sql.Tx, journalID uuid.UUID, to int) {
	if journalID != uuid.Nil {
		tx.Exec(`UPDATE journals SET next_number = $1, updated_at = NOW() WHERE id = $2`, to, journalID)
	}
}
