-- Migration 438: fn_reopen_accounting_period storno entry had no lines
-- Reference: TT Buxgalteriya ERP §4.3/§4.4 — period reopen via storno
--
-- BUG (found in the 2026-07 finance audit)
-- fn_reopen_accounting_period (migration 321) inserted the storno HEADER
-- into journal_entries — status 'posted', totals flipped from the closing
-- entry — but never inserted a single row into journal_entry_lines. The
-- result of every reopen was a 0-line posted JE that claims an amount in
-- its header while the 9xxx/9900 balances booked by the close were never
-- actually reversed in the ledger. (The 416 deferred Dt=Kt trigger does
-- not catch this: a JE with zero lines sums to 0 = 0, which counts as
-- balanced.)
--
-- THIS MIGRATION
--   PART 1 — CREATE OR REPLACE fn_reopen_accounting_period. Same
--            signature and same logic as migration 321, plus: after the
--            storno header is inserted, every line of the original
--            closing entry (closing_journal_entry_id) is mirrored into
--            journal_entry_lines with debit<->credit swapped
--            (line_number resequenced 1..N; exchange_rate, amount_base,
--            analytics_json and the analytics dimension columns copied),
--            and the header total_debit/total_credit are recomputed from
--            the inserted lines. The mirrored set is balanced whenever
--            the source closing entry was, so the 416 DEFERRABLE
--            trg_check_je_balance passes at commit.
--   PART 2 — One-time backfill: existing source_type='period_close_reopen'
--            posted entries that have ZERO lines get their lines rebuilt
--            from their reversal_of_id source entry the same way.
--            Idempotent: entries that already have lines are not touched.
--
-- accounts.current_balance IS DELIBERATELY NOT TOUCHED, in the function
-- or in the backfill: fn_close_accounting_period (321) books the closing
-- entry WITHOUT updating current_balance, so the reopen storno must not
-- update it either — doing so would introduce one-sided drift between
-- current_balance and the JE ledger (the drift class migrations 407/415
-- exist to repair). Balance recomputes are handled by those 407-style
-- ledger recomputes.
--
-- The broken reversal headers carry entry_date = the date the reopen
-- ran, and that date may now sit in a period that has since been locked
-- again — trigger trg_enforce_journal_line_invariants (319) would
-- reject the backfill inserts. session_replication_role='replica' (the
-- migration-422 pattern) needs superuser, which the runtime DB role
-- does not have, so instead the backfill temporarily unlocks exactly
-- the affected accounting_periods / fiscal_periods rows and restores
-- them at the end — all inside the single migration transaction, so no
-- intermediate state is ever visible outside it. The function itself
-- (PART 1) runs under normal trigger enforcement at runtime.

-- ============================================
-- PART 1: fixed fn_reopen_accounting_period
-- ============================================

CREATE OR REPLACE FUNCTION fn_reopen_accounting_period(
    p_closing_id UUID,
    p_user_id UUID,
    p_reason TEXT
) RETURNS BOOLEAN AS $$
DECLARE
    v_closing RECORD;
    v_reversal_je UUID;
BEGIN
    SELECT * INTO v_closing FROM period_closings WHERE id = p_closing_id;
    IF NOT FOUND OR v_closing.status <> 'closed' THEN
        RETURN false;
    END IF;

    -- Unlock the accounting_period rows
    UPDATE accounting_periods
       SET is_locked = false,
           locked_at = NULL,
           locked_by = NULL
     WHERE tenant_id = v_closing.tenant_id
       AND (v_closing.organization_id IS NULL OR organization_id = v_closing.organization_id)
       AND start_date >= v_closing.period_start
       AND end_date   <= v_closing.period_end
       AND is_locked = true;

    -- Flip fiscal_periods status back to open
    UPDATE fiscal_periods fp
       SET status = 'open',
           updated_at = NOW()
      FROM fiscal_years fy
     WHERE fp.fiscal_year_id = fy.id
       AND fy.tenant_id = v_closing.tenant_id
       AND fp.start_date >= v_closing.period_start
       AND fp.end_date   <= v_closing.period_end
       AND fp.status = 'closed';

    -- Reverse the closing entry via storno (insert a new reversing entry)
    IF v_closing.closing_journal_entry_id IS NOT NULL THEN
        v_reversal_je := uuid_generate_v4();

        INSERT INTO journal_entries (
            id, tenant_id, organization_id, journal_id, entry_number, entry_date,
            description, source_type, source_id,
            total_debit, total_credit, status,
            is_reversal, reversal_of_id, reversal_reason,
            posted_at, posted_by, created_by, created_at, updated_at
        )
        SELECT
            v_reversal_je, tenant_id, organization_id, journal_id,
            entry_number || '-REV',
            CURRENT_DATE,
            'Storno of period close ' || id::text,
            'period_close_reopen', id::text,
            total_credit,   -- flip
            total_debit,
            'posted',
            true, id, p_reason,
            NOW(), p_user_id, p_user_id, NOW(), NOW()
        FROM journal_entries
        WHERE id = v_closing.closing_journal_entry_id;

        -- Mirror every line of the original closing entry with Dt<->Kt
        -- swapped. This is what actually restores the 9xxx/9900 balances
        -- in the ledger — migration 321 omitted it, leaving 0-line
        -- posted storno entries.
        INSERT INTO journal_entry_lines (
            id, journal_entry_id, line_number, account_id,
            contact_id, warehouse_id, employee_id, contract_id, description,
            debit_amount, credit_amount, exchange_rate, amount_base,
            analytics_json, created_at
        )
        SELECT
            uuid_generate_v4(),
            v_reversal_je,
            ROW_NUMBER() OVER (ORDER BY jel.line_number, jel.id),
            jel.account_id,
            jel.contact_id, jel.warehouse_id, jel.employee_id, jel.contract_id,
            jel.description,
            jel.credit_amount,   -- flip: original Kt becomes Dt
            jel.debit_amount,    -- flip: original Dt becomes Kt
            jel.exchange_rate,
            jel.amount_base,
            jel.analytics_json,
            NOW()
        FROM journal_entry_lines jel
        WHERE jel.journal_entry_id = v_closing.closing_journal_entry_id;

        -- Recompute header totals from the actual lines (source header
        -- totals could in principle be stale; the lines are the truth)
        UPDATE journal_entries je
        SET total_debit  = (SELECT COALESCE(SUM(debit_amount), 0)  FROM journal_entry_lines WHERE journal_entry_id = je.id),
            total_credit = (SELECT COALESCE(SUM(credit_amount), 0) FROM journal_entry_lines WHERE journal_entry_id = je.id),
            updated_at = NOW()
        WHERE id = v_reversal_je;
    END IF;

    UPDATE period_closings
       SET status = 'reopened',
           reopened_at = NOW(),
           reopened_by = p_user_id,
           reopen_reason = p_reason
     WHERE id = p_closing_id;

    RETURN true;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION fn_reopen_accounting_period(UUID, UUID, TEXT) IS
    'Admin-only: reverse a period close via storno (header + mirrored Dt<->Kt lines) and unlock the period. Fixed in migration 438 — the 321 version created the storno header with no lines.';

-- ============================================
-- PART 2: backfill lines for existing 0-line reopen stornos
-- ============================================

DO $$
DECLARE
    v_rev RECORD;
    v_fixed INT := 0;
BEGIN
    -- Remember exactly which period rows we unlock so they can be
    -- restored after the inserts (ON COMMIT DROP keeps this tx-local).
    CREATE TEMP TABLE _mig438_unlocked_ap (id UUID PRIMARY KEY) ON COMMIT DROP;
    CREATE TEMP TABLE _mig438_reopened_fp (id UUID PRIMARY KEY, prev_status TEXT) ON COMMIT DROP;

    FOR v_rev IN
        SELECT rev.id AS rev_id, rev.reversal_of_id AS src_id,
               rev.tenant_id, rev.entry_date
        FROM journal_entries rev
        WHERE rev.source_type = 'period_close_reopen'
          AND rev.status = 'posted'
          AND rev.deleted_at IS NULL
          AND rev.reversal_of_id IS NOT NULL
          -- Idempotency guard: only entries with no lines at all
          AND NOT EXISTS (
              SELECT 1 FROM journal_entry_lines jel
              WHERE jel.journal_entry_id = rev.id
          )
          -- Source closing entry must have lines to mirror
          AND EXISTS (
              SELECT 1 FROM journal_entry_lines jel
              WHERE jel.journal_entry_id = rev.reversal_of_id
          )
    LOOP
        -- Trigger 319 rejects line inserts dated in a locked/closed
        -- period; unlock the periods covering this entry's date for the
        -- duration of the transaction (restored below).
        WITH u AS (
            UPDATE accounting_periods
               SET is_locked = false
             WHERE tenant_id = v_rev.tenant_id
               AND v_rev.entry_date BETWEEN start_date AND end_date
               AND is_locked = true
            RETURNING id
        )
        INSERT INTO _mig438_unlocked_ap
        SELECT id FROM u
        ON CONFLICT (id) DO NOTHING;

        -- Capture the pre-update status first (UPDATE ... RETURNING
        -- would return the new value), then flip to open.
        INSERT INTO _mig438_reopened_fp
        SELECT fp.id, fp.status
          FROM fiscal_periods fp
          JOIN fiscal_years fy ON fp.fiscal_year_id = fy.id
         WHERE fy.tenant_id = v_rev.tenant_id
           AND v_rev.entry_date BETWEEN fp.start_date AND fp.end_date
           AND fp.status IN ('locked', 'closed')
        ON CONFLICT (id) DO NOTHING;

        UPDATE fiscal_periods fp
           SET status = 'open'
          FROM fiscal_years fy
         WHERE fp.fiscal_year_id = fy.id
           AND fy.tenant_id = v_rev.tenant_id
           AND v_rev.entry_date BETWEEN fp.start_date AND fp.end_date
           AND fp.status IN ('locked', 'closed');

        INSERT INTO journal_entry_lines (
            id, journal_entry_id, line_number, account_id,
            contact_id, warehouse_id, employee_id, contract_id, description,
            debit_amount, credit_amount, exchange_rate, amount_base,
            analytics_json, created_at
        )
        SELECT
            uuid_generate_v4(),
            v_rev.rev_id,
            ROW_NUMBER() OVER (ORDER BY jel.line_number, jel.id),
            jel.account_id,
            jel.contact_id, jel.warehouse_id, jel.employee_id, jel.contract_id,
            jel.description,
            jel.credit_amount,   -- flip: original Kt becomes Dt
            jel.debit_amount,    -- flip: original Dt becomes Kt
            jel.exchange_rate,
            jel.amount_base,
            jel.analytics_json,
            NOW()
        FROM journal_entry_lines jel
        WHERE jel.journal_entry_id = v_rev.src_id;

        UPDATE journal_entries je
        SET total_debit  = (SELECT COALESCE(SUM(debit_amount), 0)  FROM journal_entry_lines WHERE journal_entry_id = je.id),
            total_credit = (SELECT COALESCE(SUM(credit_amount), 0) FROM journal_entry_lines WHERE journal_entry_id = je.id),
            updated_at = NOW()
        WHERE id = v_rev.rev_id;

        v_fixed := v_fixed + 1;
    END LOOP;

    -- Restore the locks/statuses exactly as they were before the
    -- backfill. Runs after all inserts; the deferred 416 balance
    -- trigger fires at COMMIT and does not consult period locks.
    UPDATE accounting_periods
       SET is_locked = true
     WHERE id IN (SELECT id FROM _mig438_unlocked_ap);

    UPDATE fiscal_periods fp
       SET status = r.prev_status
      FROM _mig438_reopened_fp r
     WHERE fp.id = r.id;

    RAISE NOTICE 'Migration 438: backfilled storno lines for % zero-line period_close_reopen entries', v_fixed;
END $$;

-- POST-DEPLOY VERIFICATION
-- 1) No posted period_close_reopen entry should remain without lines:
--
--   SELECT je.id, je.entry_number
--   FROM journal_entries je
--   WHERE je.source_type = 'period_close_reopen'
--     AND je.status = 'posted' AND je.deleted_at IS NULL
--     AND NOT EXISTS (SELECT 1 FROM journal_entry_lines jel
--                     WHERE jel.journal_entry_id = je.id);
--
-- 2) Every reopen storno should exactly mirror its source close entry:
--
--   SELECT rev.entry_number, rev.total_debit, rev.total_credit,
--          src.total_debit AS src_debit, src.total_credit AS src_credit
--   FROM journal_entries rev
--   JOIN journal_entries src ON src.id = rev.reversal_of_id
--   WHERE rev.source_type = 'period_close_reopen'
--     AND (rev.total_debit <> src.total_credit
--       OR rev.total_credit <> src.total_debit);
