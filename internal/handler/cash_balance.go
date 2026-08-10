package handler

import (
	"fmt"

	"github.com/google/uuid"
)

// One canonical definition of "Pul qoldig'i" (cash on hand).
//
// /reports/finance-dashboard and /reports/cash-flow used to compute this
// independently and disagreed by a fixed amount for the same tenant and period.
// Five things differed: the account set, whether is_active and is_leaf were
// filtered, whether accounts.opening_balance counted, and how organizations
// were scoped. Both are now thin callers of cashBalancesAsOf.
//
// THE DEFINITION
//
//	account set: leaf + active accounts of type CASH, plus anything flagged
//	             is_bank_account (a bank account IS cash)
//	balance:     accounts.opening_balance
//	             + SUM(debit - credit) over posted, non-deleted journal lines
//	               dated on or before the as-of date
//
// Notes on the three choices that were not simply "take one side":
//
//  1. is_leaf is not optional. Migration 317 added it precisely because "only
//     leaf accounts can be posted to"; group accounts exist purely as rollups.
//     Summing both a parent and its children double-counts every child. This is
//     the one divergence that was unambiguously a bug rather than a judgement
//     call, and cash-flow had it.
//
//  2. opening_balance counts. CreateAccount (finance.go) writes the caller's
//     opening balance straight into the column and creates NO journal entry for
//     it, so there is no double-count risk — and the dashboard, which ignored
//     the column, was understating cash by exactly the migrated balances of any
//     tenant that used it.
//
//  3. SUM(debit - credit) uniformly, with no normal_balance branching. Cash is
//     debit-normal, so this reads as "money I have" and a credit-normal account
//     in the set (an overdraft line flagged is_bank_account) correctly
//     subtracts. The old cash-flow query signed its pre-period movement by
//     at.normal_balance but its in-period movement debit-positive, so for such
//     an account the two halves of a single balance carried opposite signs.
//     Branching on normal_balance is what created that bug; not branching is
//     what fixes it.
//
// CONTRACT: this fragment hardcodes $1 for the tenant id, so every query that
// embeds it must bind the tenant as its first parameter.
// Built from cashAccountPredicate (cash_engine.go) so there is exactly ONE
// definition of "is this account cash?" in the codebase. This adds only the
// tenant/soft-delete scoping the predicate deliberately leaves to its caller.
var cashAccountSetSQL = `
	a.tenant_id = $1
	AND a.deleted_at IS NULL
	AND ` + cashAccountPredicate("a", "at")

// cashAccountBalance is one cash/bank account and its balance as of a date.
type cashAccountBalance struct {
	Code    string  `json:"code"`
	Name    string  `json:"name"`
	Balance float64 `json:"balance"`
}

// cashBalancesAsOf returns each cash account's balance as of asOf (inclusive)
// and their total. Pass uuid.Nil for orgID to span every organization.
//
// Organizations are scoped on the ACCOUNT, not on the journal entry. That is a
// deliberate departure from the original spec for this change, because the
// premise behind entry-level scoping — that a cash account is shared between
// organizations — does not hold in this schema: accounts carry
// UNIQUE(tenant_id, organization_id, code) and migration 081 gives every
// organization its own full chart of accounts. Each org already has its own
// 5010, so there is nothing shared to mis-attribute.
//
// Account-level is also the safer of the two. journal_entries.organization_id
// is ON DELETE SET NULL, so deleting an organization silently strips the org
// from its entries; an entry-level filter then drops those rows from every
// org's cash figure while the money is still sitting in an account. And for a
// BALANCE the question is which organization's account holds the cash, not
// whose paperwork moved it — if an entry belonging to org A posts a line into
// org B's till, the cash is in B.
func (h *Handler) cashBalancesAsOf(tenantID, orgID uuid.UUID, asOf string) ([]cashAccountBalance, float64, error) {
	args := []interface{}{tenantID, asOf}
	orgFilter := ""
	if orgID != uuid.Nil {
		args = append(args, orgID)
		orgFilter = fmt.Sprintf(" AND a.organization_id = $%d", len(args))
	}

	// The journal join is a LEFT JOIN onto an INNER join, not two chained LEFT
	// JOINs. Chaining them would match a line whose entry is draft, deleted or
	// after the as-of date — je would come back NULL but the LINE would still
	// be in scope, so its amounts would be summed anyway. Keeping the line and
	// entry conditions inside one inner join is what makes the date and status
	// filters actually bind.
	rows, err := h.db.Query(`
		SELECT a.code,
		       COALESCE(NULLIF(a.name_uz, ''), a.name) AS name,
		       a.opening_balance + COALESCE(SUM(l.debit_amount - l.credit_amount), 0) AS bal
		FROM accounts a
		JOIN account_types at ON at.id = a.account_type_id
		LEFT JOIN (
			journal_entry_lines l
			JOIN journal_entries je ON je.id = l.journal_entry_id
				AND je.status = 'posted' AND je.deleted_at IS NULL
				AND je.entry_date <= $2
		) ON l.account_id = a.id
		WHERE`+cashAccountSetSQL+orgFilter+`
		GROUP BY a.id, a.code, a.name_uz, a.name, a.opening_balance
		ORDER BY a.code`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	accounts := make([]cashAccountBalance, 0, 4)
	total := 0.0
	for rows.Next() {
		var ca cashAccountBalance
		if err := rows.Scan(&ca.Code, &ca.Name, &ca.Balance); err != nil {
			continue
		}
		accounts = append(accounts, ca)
		// Totalled over every account in the set, including the zero-balance
		// ones the dashboard hides, so the total never disagrees with the sum
		// of what a caller chooses to display.
		total += ca.Balance
	}
	return accounts, total, rows.Err()
}

// cashMovementBetween returns posted debit and credit totals across the cash
// account set for [from, to] inclusive. Same account set and same org scoping
// as cashBalancesAsOf, so closing = opening + (debits - credits) holds exactly.
func (h *Handler) cashMovementBetween(tenantID, orgID uuid.UUID, from, to string) (debits, credits float64, err error) {
	args := []interface{}{tenantID, from, to}
	orgFilter := ""
	if orgID != uuid.Nil {
		args = append(args, orgID)
		orgFilter = fmt.Sprintf(" AND a.organization_id = $%d", len(args))
	}

	err = h.db.QueryRow(`
		SELECT COALESCE(SUM(l.debit_amount), 0), COALESCE(SUM(l.credit_amount), 0)
		FROM accounts a
		JOIN account_types at ON at.id = a.account_type_id
		JOIN journal_entry_lines l ON l.account_id = a.id
		JOIN journal_entries je ON je.id = l.journal_entry_id
			AND je.status = 'posted' AND je.deleted_at IS NULL
			AND je.entry_date >= $2 AND je.entry_date <= $3
		WHERE`+cashAccountSetSQL+orgFilter, args...).Scan(&debits, &credits)
	return debits, credits, err
}
