-- 411_reclass_opening_balance_yoqilgi_to_xom_ashyo.sql
--
-- Migration 410 posted inventory opening-balance JEs onto account 1030
-- under the assumption (incorrect — see comment in 410) that 1030 was
-- "Xom ashyo (raw materials)" under the NAS chart of accounts. The
-- production diagnosis showed that in the standard NAS Uzbek chart:
--
--     1010 Xom ashyo va materiallar  ← actual raw materials
--     1030 Yoqilg'i                  ← fuel/petrol (NOT inventory)
--
-- As a result, after 410 ran, the inventory GL value for EVROPLIT
-- (~1.69B) and LUXURYMEBEL (~213M) was sitting on the Fuel account.
-- Buxgalteriya AKTIV roll-up looks fine in aggregate (assets total
-- still right) but it's mis-classified: business reporting that splits
-- assets by sub-type (raw materials vs. fuel vs. WIP) shows zero raw
-- materials and a fictitious fuel balance equal to total inventory.
--
-- WHAT THIS MIGRATION DOES
-- For every (tenant, org) where 410's INV-OPENING JE landed on account
-- 1030, post a *reclassification* JE:
--
--     DR  1010 Xom ashyo va materiallar     ← move value to correct account
--     CR  1030 Yoqilg'i                     ← back out from fuel
--
-- The amount moved is the EXACT debit amount from the original
-- INV-OPENING JE's line on the wrong account — not 1030's current
-- balance. This matters because Yoqilg'i may legitimately have
-- accumulated fuel transactions since the opening JE; we must only
-- reverse the opening portion.
--
-- If the org doesn't have a 1010 leaf (e.g., EVROPLIT's chart was
-- seeded without one), this migration creates it first, attached to
-- the INV account_type so it shows up in inventory reports.
--
-- The original INV-OPENING JE is left intact (audit trail). The new
-- JE has reference 'INV-RECLASS-1030-TO-1010-<tenant>-<org>' so this
-- migration is idempotent: re-running skips pairs that already have
-- the reclass entry.
--
-- TRIGGER BYPASS (same pattern as 404-410)
-- Disable the leaf-check and cash/bank-balance triggers for this
-- transaction only. PostgreSQL's transactional DDL restores them on
-- COMMIT (success or rollback).

ALTER TABLE journal_entry_lines DISABLE TRIGGER trg_enforce_journal_line_invariants;
ALTER TABLE accounts            DISABLE TRIGGER trg_check_cash_bank_balance;

DO $$
DECLARE
    pair                RECORD;
    v_opening_je_id     UUID;
    v_opening_amount    NUMERIC;
    v_wrong_acct_id     UUID;
    v_wrong_acct_code   TEXT;
    v_correct_acct_id   UUID;
    v_correct_acct_code TEXT := '1010';
    v_correct_acct_name TEXT := 'Xom ashyo va materiallar';
    v_inv_type_id       UUID;
    v_journal_id        UUID;
    v_next_seq          INT;
    v_je_id             UUID;
    v_entry_number      TEXT;
    v_reference         TEXT;
    posted_count        INT := 0;
    skipped_existing    INT := 0;
    skipped_not_1030    INT := 0;
    skipped_no_je       INT := 0;
    created_1010        INT := 0;
BEGIN
    -- INV account_type is seeded once in migration 002 (code='INV'),
    -- so this lookup is global, not per-tenant.
    SELECT id INTO v_inv_type_id
    FROM account_types
    WHERE code = 'INV'
    LIMIT 1;

    IF v_inv_type_id IS NULL THEN
        RAISE EXCEPTION 'account_types code=INV not found — cannot create 1010 leaves';
    END IF;

    FOR pair IN
        -- Every (tenant, org) that has an INV-OPENING JE from 410 (or
        -- earlier 405). Joining to journal_entries first so we only
        -- visit pairs that have something to potentially reclass.
        SELECT DISTINCT
            je.id              AS opening_je_id,
            je.organization_id AS organization_id,
            je.tenant_id       AS tenant_id
        FROM journal_entries je
        WHERE je.reference LIKE 'INV-OPENING-%'
          AND je.deleted_at IS NULL
          AND je.organization_id IS NOT NULL
    LOOP
        v_opening_je_id := pair.opening_je_id;

        -- Idempotency: skip pairs already reclassed
        v_reference := 'INV-RECLASS-1030-TO-1010-'
                    || pair.tenant_id::text
                    || '-' || pair.organization_id::text;
        IF EXISTS (
            SELECT 1 FROM journal_entries
            WHERE reference = v_reference
              AND deleted_at IS NULL
        ) THEN
            skipped_existing := skipped_existing + 1;
            CONTINUE;
        END IF;

        -- Find the DR-side line on the INV-OPENING JE — that's the
        -- account 410 picked as "inventory" (which we now know was
        -- often 1030). We pull both the account_id and the amount we
        -- need to move.
        SELECT jel.account_id, jel.debit_amount, a.code
        INTO v_wrong_acct_id, v_opening_amount, v_wrong_acct_code
        FROM journal_entry_lines jel
        JOIN accounts a ON a.id = jel.account_id
        WHERE jel.journal_entry_id = v_opening_je_id
          AND jel.debit_amount > 0
        ORDER BY jel.line_number
        LIMIT 1;

        IF v_wrong_acct_id IS NULL THEN
            skipped_no_je := skipped_no_je + 1;
            RAISE WARNING 'No DR line found on opening JE % (tenant=% org=%); skipping',
                v_opening_je_id, pair.tenant_id, pair.organization_id;
            CONTINUE;
        END IF;

        -- Only reclass if the wrong account is actually 1030. If 410
        -- already picked correctly (e.g., a tenant whose chart maps
        -- 1010 in the priority hit), do nothing.
        IF v_wrong_acct_code <> '1030' THEN
            skipped_not_1030 := skipped_not_1030 + 1;
            CONTINUE;
        END IF;

        -- Find the existing 1010 leaf for this (tenant, org). If it
        -- doesn't exist, create one attached to the INV account_type.
        -- We deliberately do NOT clone the parent_id from another
        -- account — leave it NULL so it shows as a top-level inventory
        -- leaf. Reports group by account_type/category, not parent.
        SELECT id INTO v_correct_acct_id
        FROM accounts
        WHERE tenant_id = pair.tenant_id
          AND organization_id = pair.organization_id
          AND code = v_correct_acct_code
          AND deleted_at IS NULL
        LIMIT 1;

        IF v_correct_acct_id IS NULL THEN
            INSERT INTO accounts (
                id, tenant_id, organization_id, account_type_id,
                code, name, description,
                is_active, opening_balance, current_balance,
                created_at, updated_at
            ) VALUES (
                uuid_generate_v4(),
                pair.tenant_id,
                pair.organization_id,
                v_inv_type_id,
                v_correct_acct_code,
                v_correct_acct_name,
                'Auto-created by migration 411 to reclass opening balance off 1030 (Yoqilg''i / Fuel) onto the correct raw-materials account.',
                true,
                0,
                0,
                NOW(),
                NOW()
            )
            RETURNING id INTO v_correct_acct_id;
            created_1010 := created_1010 + 1;
        END IF;

        -- Pick a journal to attach the reclass JE to (MISC/GEN/GENERAL).
        SELECT j.id, COALESCE(j.next_number, 1)
        INTO v_journal_id, v_next_seq
        FROM journals j
        WHERE j.tenant_id = pair.tenant_id
          AND j.deleted_at IS NULL
          AND j.code IN ('MISC','GEN','GENERAL','STOCK')
        ORDER BY CASE j.code
            WHEN 'MISC'    THEN 1
            WHEN 'GEN'     THEN 2
            WHEN 'GENERAL' THEN 3
            WHEN 'STOCK'   THEN 4
            ELSE 99
        END
        LIMIT 1;

        IF v_journal_id IS NULL THEN
            RAISE WARNING 'No journal found for tenant=%; cannot post reclass JE', pair.tenant_id;
            CONTINUE;
        END IF;

        v_je_id := uuid_generate_v4();
        v_entry_number := 'RCLS' || LPAD(v_next_seq::text, 6, '0');

        INSERT INTO journal_entries (
            id, tenant_id, organization_id, journal_id, entry_number,
            entry_date, reference, description,
            source_type, source_id, exchange_rate,
            total_debit, total_credit, status,
            created_at, updated_at
        ) VALUES (
            v_je_id,
            pair.tenant_id,
            pair.organization_id,
            v_journal_id,
            v_entry_number,
            CURRENT_DATE,
            v_reference,
            'Reclassify inventory opening balance from 1030 Yoqilg''i (Fuel) '
              || 'to 1010 Xom ashyo va materiallar (Raw materials). '
              || 'Corrects migration 410 mis-targeting (1030 is Fuel under '
              || 'standard NAS Uzbek chart, not raw materials). Posted by '
              || 'migration 411. Amount sourced from original INV-OPENING '
              || 'debit line, not 1030 current balance, so legitimate fuel '
              || 'activity since opening is preserved.',
            'reclassification',
            NULL,
            1.0,
            v_opening_amount,
            v_opening_amount,
            'posted',
            NOW(),
            NOW()
        );

        UPDATE journals SET next_number = next_number + 1, updated_at = NOW()
        WHERE id = v_journal_id;

        -- DR 1010 Xom ashyo (move opening into the correct account)
        INSERT INTO journal_entry_lines (
            id, journal_entry_id, line_number, account_id,
            description, debit_amount, credit_amount,
            exchange_rate, amount_base, analytics_json, created_at
        ) VALUES (
            uuid_generate_v4(), v_je_id, 1, v_correct_acct_id,
            'Reclass opening balance INTO 1010 Xom ashyo',
            v_opening_amount, 0, 1.0, v_opening_amount, '{}'::jsonb, NOW()
        );

        UPDATE accounts
        SET current_balance = current_balance + v_opening_amount, updated_at = NOW()
        WHERE id = v_correct_acct_id;

        -- CR 1030 Yoqilg'i (back the opening out of fuel)
        INSERT INTO journal_entry_lines (
            id, journal_entry_id, line_number, account_id,
            description, debit_amount, credit_amount,
            exchange_rate, amount_base, analytics_json, created_at
        ) VALUES (
            uuid_generate_v4(), v_je_id, 2, v_wrong_acct_id,
            'Reclass opening balance OUT OF 1030 Yoqilg''i',
            0, v_opening_amount, 1.0, v_opening_amount, '{}'::jsonb, NOW()
        );

        UPDATE accounts
        SET current_balance = current_balance - v_opening_amount, updated_at = NOW()
        WHERE id = v_wrong_acct_id;

        posted_count := posted_count + 1;

        RAISE NOTICE 'Reclassed % from 1030 to 1010 for tenant=% org=% (JE %)',
            v_opening_amount, pair.tenant_id, pair.organization_id, v_entry_number;
    END LOOP;

    RAISE NOTICE 'Migration 411 done: posted=% skipped_existing=% skipped_not_1030=% skipped_no_je=% created_1010_accounts=%',
        posted_count, skipped_existing, skipped_not_1030, skipped_no_je, created_1010;
END $$;

ALTER TABLE accounts            ENABLE TRIGGER trg_check_cash_bank_balance;
ALTER TABLE journal_entry_lines ENABLE TRIGGER trg_enforce_journal_line_invariants;

-- POST-DEPLOY VERIFICATION
-- After running this migration on production, expect:
--
--   EVROPLIT       1030 balance -> ~0 (was 1,690,620,292)
--                  1010 balance -> +1,595,722,927 or so (the opening amount;
--                                  410 sized opening by physical-at-the-time,
--                                  may differ slightly from today's physical)
--   LUXURYMEBEL    1030 balance -> ~0 (was 212,976,735)
--                  1010 balance -> already had 30,656,410 from PO receipts,
--                                  now += 212,976,735 = 243,633,145 or so
--
-- The org-level GL inventory SUM is unchanged (1030+1010 totals are
-- preserved), so the Ombor stat card vs Buxgalteriya AKTIV diff at
-- aggregate level is the same. What changes is sub-classification:
-- inventory now correctly appears as Xom ashyo, not Yoqilg'i.
--
-- The residual gap (EVROPLIT -304M, LUXURYMEBEL -31M) is NOT fixed by
-- this migration — that's accumulated consumption/COGS that was never
-- journalized when stock was issued from inventory. That requires either
-- (a) a periodic stock-reconciliation JE based on physical count, or
-- (b) fixing the goods-issue / production-consumption handlers to post
-- the offsetting CR-inventory JE. Tracked separately.
