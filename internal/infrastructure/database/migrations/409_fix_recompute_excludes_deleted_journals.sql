-- 409_fix_recompute_excludes_deleted_journals.sql
--
-- Re-recomputes accounts.current_balance with a correct exclusion of
-- soft-deleted journal entries. Migration 407 and migration 408 used a
-- LEFT JOIN with the deletion check in the ON clause, which fails to
-- exclude deleted JE lines from the SUM aggregation in PostgreSQL.
--
-- THE BUG IN 407/408
--
--   WITH true_balance AS (
--     SELECT a.id AS account_id,
--            SUM(jel.debit_amount) - SUM(jel.credit_amount) AS balance
--     FROM accounts a
--     LEFT JOIN journal_entry_lines jel ON jel.account_id = a.id
--     LEFT JOIN journal_entries je ON je.id = jel.journal_entry_id
--                                 AND je.status = 'posted'
--                                 AND je.deleted_at IS NULL  -- ❌
--     ...
--
-- When a journal_entries row is soft-deleted, the LEFT JOIN to it
-- produces NULL on the je.* columns — but the jel.debit_amount and
-- jel.credit_amount values stay in the result row, and SUM() still
-- aggregates them. The deletion check effectively did nothing.
--
-- Symptom: in the audited tenant, account 9690 stayed at 3,001,111,111
-- after 408 even though both contributing JEs were soft-deleted by 408
-- itself.
--
-- THE FIX
-- Use a correlated subquery that explicitly inner-joins journal_entry_lines
-- and journal_entries (so deleted JEs simply don't appear in the source
-- rowset for the SUM). The outer query loops over every non-deleted
-- account; the subquery returns 0 for accounts with no surviving JEs.

ALTER TABLE journal_entry_lines DISABLE TRIGGER trg_enforce_journal_line_invariants;
ALTER TABLE accounts            DISABLE TRIGGER trg_check_cash_bank_balance;

DO $$
DECLARE
    recomputed_count INT;
BEGIN
    -- Recompute every account's current_balance from journal_entry_lines
    -- whose parent journal_entry is posted AND not soft-deleted.
    -- Correlated subquery returns 0 for accounts with no surviving JEs,
    -- so accounts whose only entries were soft-deleted go back to zero
    -- (which is the right answer — they had no live activity).
    UPDATE accounts a
    SET current_balance = (
            SELECT COALESCE(SUM(jel.debit_amount), 0)
                 - COALESCE(SUM(jel.credit_amount), 0)
            FROM journal_entry_lines jel
            JOIN journal_entries je
                 ON je.id = jel.journal_entry_id
                AND je.status = 'posted'
                AND je.deleted_at IS NULL
            WHERE jel.account_id = a.id
        ),
        updated_at = NOW()
    WHERE a.deleted_at IS NULL
      -- Idempotency / efficiency: only touch rows whose stored value
      -- is actually different from the recomputed truth.
      AND a.current_balance IS DISTINCT FROM (
            SELECT COALESCE(SUM(jel.debit_amount), 0)
                 - COALESCE(SUM(jel.credit_amount), 0)
            FROM journal_entry_lines jel
            JOIN journal_entries je
                 ON je.id = jel.journal_entry_id
                AND je.status = 'posted'
                AND je.deleted_at IS NULL
            WHERE jel.account_id = a.id
        );

    GET DIAGNOSTICS recomputed_count = ROW_COUNT;

    RAISE NOTICE 'Migration 409 done: recomputed % account balance(s) with correct deletion handling',
        recomputed_count;
END $$;

ALTER TABLE accounts            ENABLE TRIGGER trg_check_cash_bank_balance;
ALTER TABLE journal_entry_lines ENABLE TRIGGER trg_enforce_journal_line_invariants;

-- POST-DEPLOY VERIFICATION
-- After running, the 9690 account (and any other account that was
-- carrying soft-deleted entries from migration 408) should drop to
-- its true post-deletion balance:
--
--   SELECT a.code, a.name, a.current_balance
--   FROM accounts a
--   WHERE a.code = '9690' AND a.deleted_at IS NULL;
--
--   -- All rows expected to be 0 in the audited tenant.
--
-- Also confirm the dashboard's Jami xarajat (~15M) finally agrees
-- with Buxgalteriya's XARAJAT card after this migration.
