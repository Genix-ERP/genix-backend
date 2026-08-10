package handler

// Canonical cash-account predicate, shared by every "real money" computation.
//
// The ledger's cash truth is the set of leaf, active accounts whose account
// type is CASH (kassa 5010/5020, bank 5110/5210/5220 — bank accounts are
// CASH-typed too, so an is_bank_account OR-clause only re-admits group nodes).
// Before this helper, reports.go filtered on (is_bank_account OR at.code =
// 'CASH') while finance_dashboard.go used at.code = 'CASH' alone, so the two
// could disagree the moment an account was flagged is_bank_account without the
// CASH type. Every query that needs "is this cash?" must build the condition
// from cashAccountPredicate so there is exactly one definition.

import "fmt"

// cashAccountPredicate returns the canonical SQL condition for a cash/bank
// account, using the caller's table aliases for accounts and account_types.
// The aliased tables must already be joined; the caller keeps its own
// tenant/org/deleted_at scoping.
func cashAccountPredicate(accountsAlias, typesAlias string) string {
	return fmt.Sprintf(
		"(%[2]s.code = 'CASH' AND COALESCE(%[1]s.is_leaf, true) = true AND %[1]s.is_active = true)",
		accountsAlias, typesAlias,
	)
}
