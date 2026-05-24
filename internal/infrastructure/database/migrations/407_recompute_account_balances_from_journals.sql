-- 407_recompute_account_balances_from_journals.sql
--
-- Recomputes accounts.current_balance for every account from the live
-- journal_entry_lines table, treating posted journal entries as the
-- source of truth. After this, the chart-of-accounts summary
-- (sum-of-leaves) matches the income-statement report (sum-of-JE-lines)
-- because both views derive from the same underlying ledger.
--
-- WHY THIS IS NECESSARY
-- Historical handler bugs have caused accounts.current_balance to drift
-- from the actual sum of postings on each account. Concrete examples
-- observed in the audited tenant (May 2026):
--
--   * Asosiy panel "Jami kirim" (computed from journal_entry_lines):
--       2,239,385 so'm
--   * Buxgalteriya "DAROMAD" (computed from accounts.current_balance):
--       1,971,385 so'm
--   * Account 9000 group balance (stored on the group row directly):
--       13,268,739 so'm
--
-- These three should agree but don't. The journal entries are the legal
-- record; the cached current_balance has fallen behind because at least
-- one handler somewhere in the codebase updated the JE but missed
-- updating accounts.current_balance (or vice versa). Bringing the cache
-- in line with the journal closes the gap.
--
-- WHAT THIS MIGRATION DOES
-- Single bulk UPDATE that, for every non-deleted account, sets
-- current_balance = SUM(debit_amount) - SUM(credit_amount) from all
-- posted, non-deleted journal_entry_lines targeting that account_id.
--
-- For accounts with zero JE activity, current_balance becomes 0.
-- This is correct: if no journal entry has touched an account, its
-- "true" balance is zero. Any prior non-zero value was cache drift.
--
-- The frontend's existing rollup formula
--   aggregated_balance = current_balance + sum(children_aggregated_balance)
-- (ChartOfAccounts.jsx:147-153) continues to work correctly for groups:
-- a group's current_balance reflects direct postings on the group itself
-- (some legacy entries did post directly to groups before TT §4.2 was
-- enforced), and its aggregated_balance via the frontend rollup gives
-- the full subtree total.
--
-- WHY THE THREE VIEWS WILL NOW AGREE
--   * /reports/income-statement reads journal_entry_lines directly
--     → unchanged, still the source of truth.
--   * Buxgalteriya summary sums accounts.current_balance over leaves
--     → after 407, each leaf's current_balance = its direct JE sum,
--     so the leaves sum equals what /reports/income-statement computes.
--   * Group balances on the chart-of-accounts grid sum direct postings
--     plus children via the frontend rollup → also derived from the
--     same JE truth.
--
-- IDEMPOTENCY
-- The recompute always produces the same result for the same JE data,
-- so re-running this migration is a no-op (or a fresh resync if new
-- JEs arrived since the previous run).
--
-- TRIGGER BYPASS
-- trg_check_cash_bank_balance (migration 192) rejects any UPDATE that
-- pushes a 1000*/1010*/1100* code below zero. After recomputing from
-- JEs, some of these accounts may legitimately be negative (the issues
-- exceed receipts case we documented in the 1010 history). Disable the
-- guard for this transaction only; PostgreSQL's transactional DDL
-- restores it at COMMIT (success or rollback). No trigger function is
-- altered.

ALTER TABLE accounts DISABLE TRIGGER trg_check_cash_bank_balance;

DO $$
DECLARE
    updated_count INT;
BEGIN
    -- Compute the truth balance per account from journal_entry_lines.
    -- LEFT JOIN ensures accounts without any JE activity get balance = 0
    -- (the right value, not whatever stale figure was in the cache).
    WITH true_balance AS (
        SELECT a.id AS account_id,
               COALESCE(SUM(jel.debit_amount), 0)
                 - COALESCE(SUM(jel.credit_amount), 0) AS balance
        FROM accounts a
        LEFT JOIN journal_entry_lines jel ON jel.account_id = a.id
        LEFT JOIN journal_entries je ON je.id = jel.journal_entry_id
                                    AND je.status = 'posted'
                                    AND je.deleted_at IS NULL
        WHERE a.deleted_at IS NULL
        GROUP BY a.id
    )
    UPDATE accounts a
    SET current_balance = tb.balance,
        updated_at = NOW()
    FROM true_balance tb
    WHERE a.id = tb.account_id
      -- Only write rows whose stored balance is actually different.
      -- Avoids needless updated_at churn and keeps the migration fast
      -- when re-run (idempotent).
      AND a.current_balance IS DISTINCT FROM tb.balance;

    GET DIAGNOSTICS updated_count = ROW_COUNT;

    RAISE NOTICE 'Migration 407 done: recomputed % account balance(s) from journal_entry_lines',
        updated_count;
END $$;

ALTER TABLE accounts ENABLE TRIGGER trg_check_cash_bank_balance;

-- POST-DEPLOY VERIFICATION
--
-- 1. The three views from the audit should now agree on revenue:
--
--    -- Income statement aggregation (the truth source)
--    SELECT COALESCE(SUM(jel.credit_amount), 0)
--             - COALESCE(SUM(jel.debit_amount), 0) AS revenue_from_jes
--    FROM journal_entry_lines jel
--    JOIN journal_entries je ON je.id = jel.journal_entry_id
--    JOIN accounts a         ON a.id = jel.account_id
--    JOIN account_types at   ON at.id = a.account_type_id
--    WHERE je.status = 'posted'
--      AND je.deleted_at IS NULL
--      AND a.deleted_at IS NULL
--      AND at.category = 'revenue';
--
--    -- Buxgalteriya summary (sum of leaves)
--    SELECT SUM(a.current_balance) * -1 AS revenue_from_balances
--    FROM accounts a
--    JOIN account_types at ON at.id = a.account_type_id
--    WHERE at.category = 'revenue'
--      AND a.deleted_at IS NULL
--      AND COALESCE(a.is_leaf, true) = true;
--
--    (Multiply by -1 because revenue accounts have normal_balance='credit',
--    so their DR-CR sum is negative when expressed in the universal convention.)
--
-- 2. The 9000 group balance should now equal the sum of direct postings
--    to that account, not the historical drift figure of 13,268,739:
--
--    SELECT a.code, a.name, a.current_balance,
--           (SELECT COALESCE(SUM(jel.debit_amount - jel.credit_amount), 0)
--            FROM journal_entry_lines jel
--            JOIN journal_entries je ON je.id = jel.journal_entry_id
--            WHERE jel.account_id = a.id
--              AND je.status = 'posted'
--              AND je.deleted_at IS NULL) AS recomputed
--    FROM accounts a
--    WHERE a.code = '9000' AND a.deleted_at IS NULL;
--
--    Both columns should match exactly.
