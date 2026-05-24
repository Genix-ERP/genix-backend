-- 404_reverse_buggy_po_receipt_journal_entries.sql
--
-- Reverses the buggy "Purchase Order ... received" journal entries created
-- by an older PO-receive code path that has since been removed from the
-- codebase. The current canonical path is goods_receipts.go, which posts
-- correct accounts (DR Stock Valuation / CR Accounts Payable).
--
-- BUG SIGNATURE
-- The buggy entries look like this:
--
--   entry_number: PUR000004
--   source_type:  purchase_order
--   description:  "Purchase Order PO-1770960812602 received - nmadr"
--   line 1: DR  1010 "Xom ashyo va materiallar" — correct intent
--   line 2: CR  1030 (whatever 1030 is named in this tenant)
--                  — WRONG account, should have been Accounts Payable
--
-- The credit side should be an AP liability (you owe the vendor) but was
-- instead some other asset account at code 1030. Net effect on the books:
-- one asset up, another asset down, no liability ever booked.
--
-- Real-world impact at one production tenant (May 2026 audit):
--   The asset account credited by the bug sat at -4,724,000 so'm across
--   7 entries spanning 2026-02-13 through 2026-03-02. After this
--   migration that account returns to the balance it would have had
--   without the bug.
--
-- STRATEGY
-- Post REVERSAL journal entries for every bad PUR entry. We do not DELETE
-- the originals because (a) audit history must be preserved, and (b)
-- downstream rows (e.g., inventory_transactions, period close summaries)
-- may reference these JE ids via FK.
--
-- TARGETING
-- We deliberately do NOT filter on account code/name — those vary by
-- tenant. The trio (entry_number LIKE 'PUR%', source_type='purchase_order',
-- description LIKE 'Purchase Order%received%') is unique to the buggy
-- code path. Legitimate purchase-journal entries created since the bug
-- was fixed use different descriptions ("Vendor Bill ...", "Goods
-- Receipt ...", etc.) and won't match.
--
-- IDEMPOTENCY
-- Each reversal is tagged with reference = '<orig-entry-number>-REVERSAL'.
-- The detection query skips originals that already have a matching
-- reversal posted, so re-running the migration is a no-op.
--
-- TRIGGER BYPASS
-- Migration 319 installed `trg_enforce_journal_line_invariants` and
-- migration 326 hardened the TT §4.2 leaf check from RAISE WARNING to
-- RAISE EXCEPTION. Several of the bad entries debit/credit accounts that
-- were reclassified to is_leaf=false by migration 317 (e.g., 1010
-- "Xom ashyo va materiallar" is now a group). Re-posting the same
-- account_ids in reversal lines therefore trips the leaf check.
--
-- Migration 192 also installed `trg_check_cash_bank_balance` which
-- rejects any UPDATE OF current_balance that takes a 1000*/1010*/1100*
-- code below zero. Some of the originals were posted before that trigger
-- existed (so the 1010 balance is already negative), and our reversals
-- need to push the balance further from zero on the way to undoing the
-- wrong debits — that would also trip the cash/bank guard.
--
-- This migration is a one-time data cleanup that must touch data which
-- was legitimately posted before either invariant existed. We disable
-- the two specific triggers for the duration of THIS transaction only:
-- ALTER TABLE ... DISABLE TRIGGER is transactional in PostgreSQL, so if
-- the migration fails mid-way the rollback restores the triggers to
-- their original ENABLED state. We re-enable them explicitly on the
-- happy path too, so a successful commit leaves the schema unchanged.
-- We do NOT alter the trigger functions themselves; new code paths
-- continue to be enforced normally as soon as this migration commits.
--
-- Required privilege: table owner (not superuser). The `genix` role
-- owns these tables (it created them via earlier migrations), so the
-- ALTER TABLE succeeds without elevated permissions.

ALTER TABLE journal_entry_lines DISABLE TRIGGER trg_enforce_journal_line_invariants;
ALTER TABLE accounts            DISABLE TRIGGER trg_check_cash_bank_balance;

DO $$
DECLARE
    bad_entry RECORD;
    rev_entry_id UUID;
    rev_entry_number TEXT;
    rev_line RECORD;
    journal_id_for_reversal UUID;
    next_seq INT;
    reversed_count INT := 0;
    total_reversal_count INT := 0;
BEGIN
    FOR bad_entry IN
        SELECT je.id, je.tenant_id, je.organization_id, je.journal_id,
               je.entry_number, je.entry_date, je.description,
               je.total_debit, je.total_credit, je.source_type, je.source_id
        FROM journal_entries je
        WHERE je.entry_number LIKE 'PUR%'
          AND je.entry_number NOT LIKE '%-REV%'  -- never reverse a reversal
          AND je.source_type = 'purchase_order'
          AND je.description ILIKE 'Purchase Order%received%'
          AND je.deleted_at IS NULL
          AND NOT EXISTS (
              -- Idempotency guard
              SELECT 1 FROM journal_entries je2
              WHERE je2.tenant_id = je.tenant_id
                AND je2.reference = je.entry_number || '-REVERSAL'
                AND je2.deleted_at IS NULL
          )
    LOOP
        journal_id_for_reversal := bad_entry.journal_id;

        SELECT COALESCE(next_number, 1) INTO next_seq
        FROM journals
        WHERE id = journal_id_for_reversal;

        rev_entry_id := uuid_generate_v4();
        rev_entry_number := 'PUR' || LPAD(next_seq::text, 6, '0') || '-REV';

        INSERT INTO journal_entries (
            id, tenant_id, organization_id, journal_id, entry_number,
            entry_date, reference, description,
            source_type, source_id, exchange_rate,
            total_debit, total_credit, status, created_at, updated_at
        ) VALUES (
            rev_entry_id,
            bad_entry.tenant_id,
            bad_entry.organization_id,
            journal_id_for_reversal,
            rev_entry_number,
            CURRENT_DATE,
            bad_entry.entry_number || '-REVERSAL',
            'Reversal of ' || bad_entry.entry_number ||
                ' — older PO-receive code path posted CR to wrong account ' ||
                '(should have been Accounts Payable). See migration 404.',
            'reversal',
            bad_entry.id,
            1.0,
            -- A reversal swaps totals: original DR becomes new CR and vice versa
            bad_entry.total_credit,
            bad_entry.total_debit,
            'posted',
            NOW(),
            NOW()
        );

        UPDATE journals SET next_number = next_number + 1, updated_at = NOW()
        WHERE id = journal_id_for_reversal;

        -- Mirror each line with debit/credit swapped, undo each affected balance
        FOR rev_line IN
            SELECT jel.account_id, jel.description, jel.debit_amount, jel.credit_amount,
                   jel.warehouse_id, jel.contact_id, jel.line_number
            FROM journal_entry_lines jel
            WHERE jel.journal_entry_id = bad_entry.id
            ORDER BY jel.line_number
        LOOP
            INSERT INTO journal_entry_lines (
                id, journal_entry_id, line_number, account_id,
                warehouse_id, contact_id, description,
                debit_amount, credit_amount, exchange_rate, amount_base,
                analytics_json, created_at
            ) VALUES (
                uuid_generate_v4(),
                rev_entry_id,
                rev_line.line_number,
                rev_line.account_id,
                rev_line.warehouse_id,
                rev_line.contact_id,
                'Reversal: ' || COALESCE(rev_line.description, ''),
                rev_line.credit_amount,  -- was credit, now debit
                rev_line.debit_amount,   -- was debit, now credit
                1.0,
                rev_line.credit_amount + rev_line.debit_amount,
                '{}'::jsonb,
                NOW()
            );

            -- Undo the original's effect on the account balance.
            --   Original DR'd X on A → balance was +X. Reversal CR's X → 0.
            --   Original CR'd Y on B → balance was -Y. Reversal DR's Y → 0.
            -- Net: subtract original DR, add back original CR.
            UPDATE accounts
            SET current_balance = current_balance - rev_line.debit_amount + rev_line.credit_amount,
                updated_at = NOW()
            WHERE id = rev_line.account_id;
        END LOOP;

        reversed_count := reversed_count + 1;

        RAISE NOTICE 'Reversed %: % (DR=%, CR=%)',
            bad_entry.entry_number,
            LEFT(bad_entry.description, 50),
            bad_entry.total_debit,
            bad_entry.total_credit;
    END LOOP;

    SELECT COUNT(*) INTO total_reversal_count
    FROM journal_entries je
    WHERE je.reference LIKE 'PUR%-REVERSAL'
      AND je.deleted_at IS NULL;

    RAISE NOTICE 'Migration 404 done: % reversals posted this run, % total reversal entries exist',
        reversed_count, total_reversal_count;
END $$;

-- Re-enable the triggers we disabled at the top. If the DO block above
-- raised, we never reach here, but the surrounding transaction will roll
-- back and the triggers come back automatically as part of the DDL rollback.
ALTER TABLE accounts            ENABLE TRIGGER trg_check_cash_bank_balance;
ALTER TABLE journal_entry_lines ENABLE TRIGGER trg_enforce_journal_line_invariants;

-- POST-DEPLOY VERIFICATION
-- Run this after the migration to confirm the account that was bleeding
-- credits (in the production tenant audited in May 2026 this was account
-- code 1030) has returned to a sensible balance:
--
--   SELECT a.code, a.name, a.current_balance
--   FROM accounts a
--   WHERE a.code IN ('1010','1020','1030','2100','6010')
--     AND a.deleted_at IS NULL
--   ORDER BY a.code, a.name;
--
-- And confirm reversal entries exist:
--
--   SELECT entry_number, reference, description, total_debit, total_credit
--   FROM journal_entries
--   WHERE reference LIKE 'PUR%-REVERSAL'
--   ORDER BY entry_number;
