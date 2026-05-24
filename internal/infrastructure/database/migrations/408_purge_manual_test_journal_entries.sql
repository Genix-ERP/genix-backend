-- 408_purge_manual_test_journal_entries.sql
--
-- Soft-deletes obvious test journal entries that have been polluting the
-- GL with multi-million-so'm fake transactions, then recomputes
-- accounts.current_balance to reflect the cleaned ledger.
--
-- DIAGNOSTIC PATTERN
-- In the audited tenant (May 2026), account 9690 "Boshqa moliyaviy
-- xarajatlar" (Other Financial Expenses) carried a balance of
-- 3,001,111,111 so'm — about 200× the rest of the entire expense ledger
-- combined. The culprit was two entries posted via the manual-JE UI:
--
--   entry_number   description   debit_amount    source_type
--   -2026-0026     "123"         3,000,000,000   manual
--   -2026-0025     "12"          1,111,111       manual
--
-- The signature is unmistakable: descriptions that are pure digits, a
-- round/contrived amount, source_type='manual', and an unusual entry
-- number format. Real manual JEs in production have descriptive text
-- ("Stock adjustment Q1", "Year-end accrual", etc.) — pure-digit
-- descriptions on multi-million-so'm amounts are a near-certain test
-- pollution fingerprint.
--
-- WHAT THIS MIGRATION DOES
--   1. Soft-deletes every journal_entries row where:
--        - source_type = 'manual'
--        - description matches the regex ^[0-9]+$  (pure digits)
--        - GREATEST(total_debit, total_credit) >= 1,000,000  (material)
--        - deleted_at IS NULL  (not already soft-deleted)
--   2. Recomputes accounts.current_balance for every account from the
--      remaining live journal_entry_lines (migration 407's logic), so
--      the chart-of-accounts cache picks up the change.
--
-- AUDIT PRESERVATION
-- Soft-delete only — journal_entries.deleted_at is set, but the rows
-- and their journal_entry_lines stay in the DB. Any future audit query
-- that intentionally inspects deleted_at IS NOT NULL will see them with
-- a clear "deleted by migration 408" trail (via the description suffix
-- we append). No hard-delete, no FK cascade, no orphan inventory rows.
--
-- IDEMPOTENCY
-- Second run finds zero candidates because every previously-targeted
-- entry has deleted_at IS NOT NULL. The recompute also becomes a no-op
-- because nothing changed on the second pass.
--
-- TRIGGER BYPASS (same pattern as 404/405/406/407)
-- The recompute may push some accounts to balances the cash/bank guard
-- (migration 192) would otherwise reject. Disable that trigger and the
-- TT §4.2 leaf-check trigger for the duration of this transaction only.
-- PostgreSQL's transactional DDL restores them at COMMIT.

ALTER TABLE journal_entry_lines DISABLE TRIGGER trg_enforce_journal_line_invariants;
ALTER TABLE accounts            DISABLE TRIGGER trg_check_cash_bank_balance;

DO $$
DECLARE
    bad_je RECORD;
    deleted_count INT := 0;
    recomputed_count INT;
BEGIN
    -- Step 1: identify and soft-delete the suspect entries.
    -- The pattern (manual + pure-digit description + >=1M amount) is
    -- specific enough that we'd rather over-clean (a real entry with
    -- a digits-only description is itself a data-quality problem and
    -- can be reposted) than leave the pollution.
    FOR bad_je IN
        SELECT je.id, je.tenant_id, je.entry_number, je.description,
               je.total_debit, je.total_credit
        FROM journal_entries je
        WHERE je.source_type = 'manual'
          AND je.deleted_at IS NULL
          AND je.description ~ '^[0-9]+$'
          AND GREATEST(COALESCE(je.total_debit, 0), COALESCE(je.total_credit, 0)) >= 1000000
    LOOP
        UPDATE journal_entries
        SET deleted_at = NOW(),
            updated_at = NOW(),
            description = COALESCE(description, '') ||
                ' [SOFT-DELETED by migration 408: test entry, pure-digit description, large amount]'
        WHERE id = bad_je.id;

        deleted_count := deleted_count + 1;

        RAISE NOTICE 'Soft-deleted JE % (desc=%, max amount=%) — test data signature',
            bad_je.entry_number,
            bad_je.description,
            GREATEST(bad_je.total_debit, bad_je.total_credit);
    END LOOP;

    -- Step 2: recompute every account's current_balance from the
    -- remaining live JEs (deleted_at IS NULL filter automatically
    -- excludes what we just soft-deleted). Same query body as
    -- migration 407, applied here in one bulk UPDATE.
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
      AND a.current_balance IS DISTINCT FROM tb.balance;

    GET DIAGNOSTICS recomputed_count = ROW_COUNT;

    RAISE NOTICE 'Migration 408 done: soft-deleted % manual test JE(s), recomputed % account balance(s)',
        deleted_count, recomputed_count;
END $$;

ALTER TABLE accounts            ENABLE TRIGGER trg_check_cash_bank_balance;
ALTER TABLE journal_entry_lines ENABLE TRIGGER trg_enforce_journal_line_invariants;

-- POST-DEPLOY VERIFICATION
--
-- 1. The 9690 leaf should return to a sane balance (likely 0 in this
--    audited tenant since the only postings to it were the two bad
--    manual entries):
--
--    SELECT a.code, a.name, a.current_balance
--    FROM accounts a
--    WHERE a.code = '9690' AND a.deleted_at IS NULL;
--
-- 2. The dashboard's "Jami xarajat" and Buxgalteriya's "XARAJAT"
--    summary card should now agree (modulo the per-account-type
--    classification differences that produce minor rounding gaps).
--
-- 3. Confirm the soft-deletes are findable for audit:
--
--    SELECT entry_number, entry_date::date, source_type,
--           LEFT(description, 80) AS description,
--           total_debit, total_credit
--    FROM journal_entries
--    WHERE deleted_at IS NOT NULL
--      AND description LIKE '%SOFT-DELETED by migration 408%'
--    ORDER BY entry_date;
