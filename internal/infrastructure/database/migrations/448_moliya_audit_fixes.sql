-- 448_moliya_audit_fixes.sql
--
-- Finance module audit fixes (docs/moliya-audit.md, 2026-08-02):
--
--   1. Void zero-line "posted" JE artifacts. The pre-2026-07-13 autocommit
--      bug left posted headers whose lines were rejected by the deferred
--      balance trigger (EXP000001/2, 20260217 in the dev tenant). A posted
--      header with no lines is not a document — it inflates report queries
--      that trust total_debit/total_credit and cannot be reversed (nothing
--      to reverse). Mark them cancelled.
--
--   2. Re-run the migration-407 balance recompute. current_balance drifted
--      again: the zero-line JEs' phantom `UPDATE accounts` legs (+2 240 000
--      on 6010, +400 000 on 9410), ~20 mln legacy drift on 5010/5110, and
--      mixed sign conventions (9010 stored credit-positive, 6410 stored
--      debit-positive). Convention after this migration, same as 407:
--      current_balance = SUM(debit_amount) - SUM(credit_amount) over posted,
--      non-deleted entries, for every account regardless of nature.
--
--   3. Map seeded expense categories to the differentiated 94xx leaf
--      accounts (created by the chart seed, linked by nothing). With
--      account_id NULL, every expense payment takes the 9410 fallback in
--      PayExpense — which is why the dashboard donut has exactly two
--      slices. Mapping (per tenant, first organization's chart):
--        IJARA     -> 9430  Ijara xarajatlari
--        KOMMUNAL  -> 9440  Kommunal xarajatlar
--        OFIS      -> 9450  Ofis xarajatlari
--        REKLAMA   -> 9480  Reklama va marketing xarajatlari
--        TRANSPORT -> 9490  Boshqa xizmatlar uchun xarajatlar
--        SAFAR     -> 9490  Boshqa xizmatlar uchun xarajatlar
--        MATERIAL  -> 9410  Davr xarajatlari
--        BOSHQA    -> 9410  Davr xarajatlari
--      Only fills NULLs — a tenant who mapped a category by hand keeps
--      their choice. New tenants get the same mapping from the Go lazy
--      seed (expense.go seedDefaultExpenseCategories).

-- Step 1 flips zero-line posted JEs to 'cancelled', which the TT 4.4 invariant
-- trigger (migration 319/326) otherwise rejects ("posted entries cannot be
-- reverted to draft/cancelled without a storno"). These are not real documents
-- — they have no lines, so there is nothing to storno. Suppress triggers for
-- this one-time cleanup transaction only, exactly as migration 422 does. This
-- also lets step 2's balance recompute set absolute values without tripping the
-- cash/bank non-negative guard. SET LOCAL auto-reverts at COMMIT; we also reset
-- explicitly at the end. Requires superuser (POSTGRES_USER is one).
SET LOCAL session_replication_role = 'replica';

-- 1. Void zero-line posted JEs -------------------------------------------
UPDATE journal_entries je
SET status = 'cancelled', updated_at = NOW()
WHERE je.status = 'posted'
  AND je.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM journal_entry_lines l WHERE l.journal_entry_id = je.id
  );

-- 2. Recompute cached balances from the ledger ---------------------------
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

-- 3. Map expense categories to 94xx leaf accounts ------------------------
WITH mapping(cat_code, acct_code) AS (
    VALUES ('IJARA',     '9430'),
           ('KOMMUNAL',  '9440'),
           ('OFIS',      '9450'),
           ('REKLAMA',   '9480'),
           ('TRANSPORT', '9490'),
           ('SAFAR',     '9490'),
           ('MATERIAL',  '9410'),
           ('BOSHQA',    '9410')
),
-- One account per (tenant, code): the first organization's chart, matching
-- the lazy-seed resolution order in Go.
acct AS (
    SELECT DISTINCT ON (a.tenant_id, a.code)
           a.tenant_id, a.code, a.id
    FROM accounts a
    WHERE a.deleted_at IS NULL
      AND a.is_active = true
      AND a.is_leaf = true
      AND a.code IN ('9410', '9430', '9440', '9450', '9480', '9490')
    ORDER BY a.tenant_id, a.code, a.created_at
)
UPDATE expense_categories ec
SET account_id = acct.id, updated_at = NOW()
FROM mapping m
JOIN acct ON acct.code = m.acct_code
WHERE ec.code = m.cat_code
  AND ec.tenant_id = acct.tenant_id
  AND ec.account_id IS NULL;

-- Restore normal trigger behaviour for the remainder of the transaction (the
-- schema_migrations insert). SET LOCAL would also auto-revert at COMMIT.
SET LOCAL session_replication_role = DEFAULT;
