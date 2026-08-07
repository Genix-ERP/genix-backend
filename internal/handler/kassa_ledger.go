package handler

// One canonical definition of what moves money through a kassa.
//
// Three surfaces disagreed about a till's balance because each read a different
// table, and none of them saw everything:
//
//	                        GL / dashboard   cash_book_entries   cash_transactions
//	confirmed PKO/RKO             yes              yes                  no
//	cash transaction              no               no                   yes
//
// So the web (which summed cash_transactions) missed every PKO/RKO, mobile
// (which read cash_book_entries) missed every cash transaction, and the finance
// dashboard missed the transactions entirely because CreateCashTransaction
// never posts a journal entry.
//
// kassaMovementSQL is the union of both event sources, and it is now the only
// thing the cash book and the balance guard read. Deriving from the events
// rather than maintaining a materialised cash_book_entries table also removes a
// whole class of drift: cash_transactions are HARD-deleted (DELETE FROM, not a
// soft flag), so any running total kept in a side table would silently keep the
// deleted rows forever. A derived total cannot go stale.
//
// CONTRACT: $1 is the tenant id. Callers append their own predicates and must
// bind the tenant first.
const kassaMovementSQL = `
	SELECT co.cash_register_id AS register_id,
	       co.order_date       AS d,
	       CASE WHEN co.order_type = 'pko' THEN co.amount ELSE 0 END AS income,
	       CASE WHEN co.order_type = 'rko' THEN co.amount ELSE 0 END AS expense
	FROM cash_orders co
	WHERE co.tenant_id = $1
	  AND co.deleted_at IS NULL
	  AND co.status = 'confirmed'

	UNION ALL

	SELECT ct.cash_register_id AS register_id,
	       ct.transaction_date AS d,
	       -- Two vocabularies live in this column: migration 004 documents
	       -- 'receipt'/'disbursement' while the app writes 'income'/'expense'.
	       -- Both are accepted so historical rows are not silently counted as
	       -- the wrong direction — the old balance query matched only 'income'
	       -- and treated everything else, including every receipt, as negative.
	       CASE WHEN ct.transaction_type IN ('income', 'receipt')       THEN ct.amount ELSE 0 END AS income,
	       CASE WHEN ct.transaction_type IN ('expense', 'disbursement') THEN ct.amount ELSE 0 END AS expense
	FROM cash_transactions ct
	WHERE ct.tenant_id = $1
	  AND COALESCE(ct.status, 'posted') <> 'cancelled'
`

// cash_transactions has NO deleted_at column (migration 004 defines the table;
// 036 only adds category/cashier/currency and relaxes account_id). Filtering on
// it — as the old negative-balance guard did — makes Postgres reject the whole
// statement, and because that query discarded its error the balance silently
// came back 0, so `0 < amount` rejected EVERY cash expense with "Kassada
// mablag' yetarli emas (balans: 0.00)". Do not reintroduce it here.

// kassaBalance returns a till's balance from every event that touched it.
// Pass uuid.Nil-equivalent "" for registerID to span every till in the tenant.
func (h *Handler) kassaBalance(tenantID interface{}, registerID string) (float64, error) {
	args := []interface{}{tenantID}
	filter := ""
	if registerID != "" {
		args = append(args, registerID)
		filter = " AND mv.register_id = $2"
	}

	var balance float64
	err := h.db.QueryRow(`
		SELECT COALESCE(SUM(mv.income - mv.expense), 0)
		FROM (`+kassaMovementSQL+`) mv
		WHERE TRUE`+filter, args...).Scan(&balance)
	return balance, err
}
