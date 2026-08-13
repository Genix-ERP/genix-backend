-- 493: recompute accounts.current_balance from the ledger, once more.
--
-- The 2026-08-13 business-logic audit found ~21 balance writers violating the
-- 407/448 convention (current_balance = SUM(debit) − SUM(credit) over posted,
-- non-deleted entries, for EVERY account): SendInvoice added income credits,
-- CreateBillFromPO added AP credits, purchase/sales return debit legs
-- subtracted, the dividend posting flipped two of its three legs, the
-- "repair" endpoint re-injected the drift it was named after, and two
-- targeted recomputes plus the vipiska import used a normal_balance branch
-- that negates credit-normal accounts. All writers are fixed in the same
-- release as this migration; this recompute erases the drift they left
-- behind. The journal lines themselves were always correct — only the cached
-- balance drifted — so this is a pure cache rebuild, identical in shape to
-- migration 448's.

WITH sums AS (
    SELECT jel.account_id,
           SUM(jel.debit_amount)  AS total_debit,
           SUM(jel.credit_amount) AS total_credit
    FROM journal_entry_lines jel
    JOIN journal_entries je ON je.id = jel.journal_entry_id
    WHERE je.status = 'posted'
      AND je.deleted_at IS NULL
    GROUP BY jel.account_id
)
UPDATE accounts a
SET current_balance = COALESCE(s.total_debit, 0) - COALESCE(s.total_credit, 0),
    updated_at = NOW()
FROM (
    SELECT acc.id, sums.total_debit, sums.total_credit
    FROM accounts acc
    LEFT JOIN sums ON sums.account_id = acc.id
    WHERE acc.deleted_at IS NULL
) s
WHERE a.id = s.id
  AND a.current_balance IS DISTINCT FROM (COALESCE(s.total_debit, 0) - COALESCE(s.total_credit, 0));
