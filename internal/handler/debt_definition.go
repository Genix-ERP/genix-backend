package handler

import "fmt"

// What "owed" means, in one place.
//
// This existed in five: the finance dashboard card, the AR aging report, the
// AP aging report, the director's per-organization debtors/creditors, and the
// director's overdue receivables — each written separately, and no two of them
// agreeing. The same tenant read 58 944 032 on one screen and -46 570 968 on
// another.
//
// Two rules make up the definition, and both were violated somewhere:
//
//  1. STATUS. A draft is not a debt. Aging filtered only 'cancelled', so every
//     invoice nobody had advanced counted as money owed — and both status
//     columns DEFAULT to 'draft'. The director's overdue query went the other
//     way and accepted only ('sent','partial'), which silently drops an
//     invoice whose status is 'overdue' out of the overdue figure.
//
//  2. NETTING. Debt is netted per PARTNER, not per invoice. amount_due is
//     generated as total_amount - amount_paid, nothing caps amount_paid, so an
//     overpaid invoice carries a negative amount_due. Filtering `amount_due >
//     0` row by row therefore does not mean "still owed" — it throws the
//     overpayment away, and a customer who overpaid one invoice while owing on
//     another is reported at the second one's full value.
//
// The 0.005 epsilon matches the `due` threshold in payments_reconcile.go
// rather than comparing a float sum against zero.

// debtStatusFilter is the status vocabulary shared by every debt query.
const debtStatusFilter = `status NOT IN ('draft', 'cancelled', 'void')`

// partnerNetDue builds a subquery of one row per partner, carrying that
// partner's net `due` and net `overdue`.
//
// Callers wrap it and aggregate; they must filter on `due > 0.005` (or
// `overdue > 0.005`) in the outer query rather than inside, because a partner
// in net credit has to be excluded from the total AFTER their invoices are
// summed, not before.
//
//	table       sales_invoices | purchase_invoices
//	partnerCol  customer_id    | vendor_id
//	groupExtra  additional grouping column, e.g. "organization_id" — emitted
//	            first in both the SELECT and the GROUP BY, or "" for none
//	extraWhere  additional predicates, already carrying their own placeholders
func partnerNetDue(table, partnerCol, groupExtra, extraWhere string) string {
	lead := ""
	if groupExtra != "" {
		lead = groupExtra + ", "
	}
	return fmt.Sprintf(`
		SELECT %[1]s%[2]s AS partner_id,
		       SUM(amount_due) AS due,
		       SUM(CASE WHEN due_date < CURRENT_DATE THEN amount_due ELSE 0 END) AS overdue
		FROM %[3]s
		WHERE tenant_id = $1 AND deleted_at IS NULL
		  AND %[2]s IS NOT NULL
		  AND %[4]s%[5]s
		GROUP BY %[1]s%[2]s`,
		lead, partnerCol, table, debtStatusFilter, extraWhere)
}
