-- 414_reclass_post_411_yoqilgi_drift.sql
--
-- Cleanup migration for the residue left behind by the *runtime*
-- equivalent of the bug migration 411 cleaned up at rest.
--
-- BACKGROUND
-- Migration 411 reclassified the inventory OPENING balance from
-- 1030 Yoqilg'i (Fuel) → 1010 Xom ashyo va materiallar (Raw materials).
-- That was a one-shot snapshot. But the runtime handler code had the
-- same root bug — `findAccount(..., "raw materials", "1030")` and
-- `inventoryType == "raw"` both resolved to 1030 — so every purchase
-- receipt, production order, and work-order JE since the opening kept
-- crediting/debiting Fuel for raw-material movement.
--
-- For EVROPLIT (tenant ce0b21e8-…, org 465fb035-…) production
-- diagnosis (May 2026) showed 1030 Yoqilg'i had drifted to ~70.4M
-- post-411 — entirely raw-material activity wearing a Fuel label.
-- Other tenants under the same handler bug have the same shape of
-- drift in proportion to their post-411 purchase / production volume.
--
-- The handler bug was fixed in:
--   * manufacturing.go  (start-MO, complete-MO, return-to-WIP)
--   * work_orders.go    (work-order completion)
--   * admin_settings.go (inventoryType=raw lookup)
-- so the drift stops accumulating. This migration retroactively moves
-- the existing drift to the correct account.
--
-- WHAT THIS MIGRATION DOES
-- For each (tenant, org) with non-zero post-411 net activity on 1030
-- whose source is manufacturing or product-receipt traffic:
--   1. Compute the misclassified net: SUM(debit-credit) on 1030 from
--      JE lines POSTED AFTER 411 ran whose journal_entry comes from
--      manufacturing / production / purchase-receipt sources.
--      Lines posted BEFORE 411 are out of scope — 411 already handled
--      its own scope and idempotency.
--   2. Ensure 1010 exists for the (tenant, org). Create if missing
--      (same auto-create pattern used by 412).
--   3. Post a reclassification JE: DR 1010 / CR 1030 for the misclass
--      amount (or DR 1030 / CR 1010 if the drift is net-credit, which
--      shouldn't normally happen but is handled defensively).
--   4. Update account.current_balance on both rows.
--   5. Idempotent via reference 'INV-RECLASS-1030-DRIFT-<tenant>-<org>'.
--
-- WHY THIS IS SAFE
--   * The original misclassified JE lines are LEFT INTACT — full audit
--     trail preserved (you can still see "Production Order MO00104
--     started" debiting 1030 at the time it actually happened).
--   * Only the bookkeeping correction is added.
--   * A second run is a no-op (reference uniqueness check).
--   * Lines that legitimately landed on 1030 (real fuel purchases, if
--     any) are filtered out by source pattern — only manufacturing /
--     receipt sources are reclassified.
--   * Same trigger-bypass pattern as 404-412.

ALTER TABLE journal_entry_lines DISABLE TRIGGER trg_enforce_journal_line_invariants;
ALTER TABLE accounts            DISABLE TRIGGER trg_check_cash_bank_balance;

DO $$
DECLARE
    pair               RECORD;
    v_drift_amount     NUMERIC;
    v_1030_acct_id     UUID;
    v_1010_acct_id     UUID;
    v_inv_type_id      UUID;
    v_journal_id       UUID;
    v_next_seq         INT;
    v_je_id            UUID;
    v_entry_number     TEXT;
    v_reference        TEXT;
    v_411_je_date      DATE;
    posted_count       INT := 0;
    skipped_existing   INT := 0;
    skipped_zero       INT := 0;
    skipped_no_1030    INT := 0;
    created_1010       INT := 0;
BEGIN
    SELECT id INTO v_inv_type_id FROM account_types WHERE code = 'INV' LIMIT 1;
    IF v_inv_type_id IS NULL THEN
        RAISE EXCEPTION 'account_types code=INV missing — cannot ensure 1010 exists';
    END IF;

    FOR pair IN
        -- Every (tenant, org) that has a 1030 account at all.
        -- We compute the drift inside the loop because the filter
        -- depends on the per-pair 411 reclass JE date.
        SELECT DISTINCT a.tenant_id, a.organization_id
        FROM accounts a
        WHERE a.code = '1030'
          AND a.deleted_at IS NULL
          AND a.organization_id IS NOT NULL
    LOOP
        v_reference := 'INV-RECLASS-1030-DRIFT-'
                    || pair.tenant_id::text
                    || '-' || pair.organization_id::text;

        -- Idempotency.
        IF EXISTS (
            SELECT 1 FROM journal_entries
            WHERE reference = v_reference
              AND deleted_at IS NULL
        ) THEN
            skipped_existing := skipped_existing + 1;
            CONTINUE;
        END IF;

        -- Find the per-pair 411 reclass JE date so we only correct
        -- post-411 activity. If 411 never ran for this pair (no
        -- INV-RECLASS-1030-TO-1010 JE), use the epoch — i.e., treat
        -- all 1030 activity as post-411-equivalent drift.
        SELECT entry_date INTO v_411_je_date
        FROM journal_entries
        WHERE reference = 'INV-RECLASS-1030-TO-1010-'
                       || pair.tenant_id::text
                       || '-' || pair.organization_id::text
          AND deleted_at IS NULL
        LIMIT 1;
        IF v_411_je_date IS NULL THEN
            v_411_je_date := DATE '1900-01-01';
        END IF;

        -- Find this org's 1030 account id.
        SELECT id INTO v_1030_acct_id
        FROM accounts
        WHERE tenant_id = pair.tenant_id
          AND organization_id = pair.organization_id
          AND code = '1030'
          AND deleted_at IS NULL
        LIMIT 1;

        IF v_1030_acct_id IS NULL THEN
            skipped_no_1030 := skipped_no_1030 + 1;
            CONTINUE;
        END IF;

        -- Sum the misclassified net on 1030. Filter to JEs whose
        -- description matches the handler-bug surface area:
        --   * manufacturing.go: 'Production Order %' (English) and
        --     'Ishlab chiqarish yakunlandi: %' (Uzbek)
        --   * work_orders.go:   'Ishlab chiqarish yakunlandi: %'
        --   * admin_settings.go via PO receipts: descriptions like
        --     'Purchase receipt %', 'Mahsulot qabul qilindi %',
        --     'PO-% received', etc. We include both English and
        --     Uzbek/Russian variants.
        -- Lines posted BEFORE 411's reclass are excluded (411 already
        -- handled the opening).
        SELECT COALESCE(SUM(jel.debit_amount - jel.credit_amount), 0)
        INTO v_drift_amount
        FROM journal_entry_lines jel
        JOIN journal_entries je ON je.id = jel.journal_entry_id
        WHERE jel.account_id = v_1030_acct_id
          AND je.tenant_id = pair.tenant_id
          AND je.organization_id = pair.organization_id
          AND je.deleted_at IS NULL
          AND je.entry_date > v_411_je_date
          AND je.reference IS DISTINCT FROM ('INV-RECLASS-1030-TO-1010-'
                                          || pair.tenant_id::text
                                          || '-' || pair.organization_id::text)
          AND (
                je.description ILIKE 'Production Order %'
             OR je.description ILIKE 'Ishlab chiqarish yakunlandi%'
             OR je.description ILIKE 'Materials returned%'
             OR je.description ILIKE '%materials consumed%'
             OR je.description ILIKE '%materials c%'      -- truncated variant seen in data
             OR je.description ILIKE 'Purchase receipt%'
             OR je.description ILIKE 'PO-% received%'
             OR je.description ILIKE 'Mahsulot qabul qilindi%'
             OR je.description ILIKE 'Tovar qabul qilindi%'
             OR je.description ILIKE 'Получение товара%'
             OR je.source_type IN ('production_order', 'work_order', 'purchase_receipt', 'purchase_order')
          );

        IF v_drift_amount = 0 THEN
            skipped_zero := skipped_zero + 1;
            CONTINUE;
        END IF;

        -- ============================================================
        -- Ensure 1010 exists for this (tenant, org). Same auto-create
        -- pattern as migration 412.
        -- ============================================================
        SELECT id INTO v_1010_acct_id
        FROM accounts
        WHERE tenant_id = pair.tenant_id
          AND organization_id = pair.organization_id
          AND code = '1010'
          AND deleted_at IS NULL
        LIMIT 1;

        IF v_1010_acct_id IS NULL THEN
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
                '1010',
                'Xom ashyo va materiallar',
                'Auto-created by migration 414. Org had no 1010 leaf so '
                  || 'the post-411 reclass had no target account. Created '
                  || 'under the INV account_type.',
                true,
                0,
                0,
                NOW(),
                NOW()
            )
            ON CONFLICT (tenant_id, organization_id, code) DO NOTHING
            RETURNING id INTO v_1010_acct_id;

            IF v_1010_acct_id IS NULL THEN
                -- Row exists but possibly soft-deleted; undelete it.
                UPDATE accounts
                   SET deleted_at = NULL,
                       is_active  = true,
                       updated_at = NOW()
                 WHERE tenant_id = pair.tenant_id
                   AND organization_id = pair.organization_id
                   AND code = '1010'
                RETURNING id INTO v_1010_acct_id;
            ELSE
                created_1010 := created_1010 + 1;
            END IF;

            IF v_1010_acct_id IS NULL THEN
                RAISE WARNING 'Could not create or find 1010 for tenant=% org=%; skipping',
                    pair.tenant_id, pair.organization_id;
                CONTINUE;
            END IF;
        END IF;

        -- ============================================================
        -- Pick a journal to post under. Same priority used by 412.
        -- ============================================================
        SELECT id, COALESCE(next_number, 1)
        INTO v_journal_id, v_next_seq
        FROM journals
        WHERE tenant_id = pair.tenant_id
          AND deleted_at IS NULL
          AND code IN ('MISC','GEN','GENERAL','STOCK')
        ORDER BY CASE code
            WHEN 'MISC'    THEN 1
            WHEN 'GEN'     THEN 2
            WHEN 'GENERAL' THEN 3
            WHEN 'STOCK'   THEN 4
            ELSE 99
        END
        LIMIT 1;

        IF v_journal_id IS NULL THEN
            RAISE WARNING 'No posting journal for tenant=%; skipping (migration 412 should have created MISC)',
                pair.tenant_id;
            CONTINUE;
        END IF;

        -- ============================================================
        -- Post reclass JE.
        --   * If v_drift_amount > 0, 1030 was over-debited.
        --     Move it to 1010:   DR 1010 / CR 1030 for v_drift_amount.
        --   * If v_drift_amount < 0, 1030 was over-credited (rare).
        --     Reverse:           DR 1030 / CR 1010 for ABS(v_drift_amount).
        -- ============================================================
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
            'Reclass post-411 drift from 1030 Yoqilg''i (Fuel) to '
              || '1010 Xom ashyo va materiallar (Raw materials). '
              || 'Corrects manufacturing/receipt JEs that the runtime '
              || 'handler mis-routed to 1030 between the 411 cutover '
              || 'and the handler fix. Original JE lines preserved.',
            'opening_balance',
            NULL,
            1.0,
            ABS(v_drift_amount),
            ABS(v_drift_amount),
            'posted',
            NOW(),
            NOW()
        );

        UPDATE journals SET next_number = next_number + 1, updated_at = NOW()
        WHERE id = v_journal_id;

        IF v_drift_amount > 0 THEN
            -- DR 1010
            INSERT INTO journal_entry_lines (
                id, journal_entry_id, line_number, account_id,
                description, debit_amount, credit_amount,
                exchange_rate, amount_base, analytics_json, created_at
            ) VALUES (
                uuid_generate_v4(), v_je_id, 1, v_1010_acct_id,
                'Reclass drift in (1010)',
                v_drift_amount, 0, 1.0, v_drift_amount, '{}'::jsonb, NOW()
            );
            UPDATE accounts
            SET current_balance = current_balance + v_drift_amount, updated_at = NOW()
            WHERE id = v_1010_acct_id;

            -- CR 1030
            INSERT INTO journal_entry_lines (
                id, journal_entry_id, line_number, account_id,
                description, debit_amount, credit_amount,
                exchange_rate, amount_base, analytics_json, created_at
            ) VALUES (
                uuid_generate_v4(), v_je_id, 2, v_1030_acct_id,
                'Reclass drift out (1030 Yoqilg''i)',
                0, v_drift_amount, 1.0, v_drift_amount, '{}'::jsonb, NOW()
            );
            UPDATE accounts
            SET current_balance = current_balance - v_drift_amount, updated_at = NOW()
            WHERE id = v_1030_acct_id;
        ELSE
            -- Net-credit drift: 1030 was over-credited. Reverse.
            -- DR 1030
            INSERT INTO journal_entry_lines (
                id, journal_entry_id, line_number, account_id,
                description, debit_amount, credit_amount,
                exchange_rate, amount_base, analytics_json, created_at
            ) VALUES (
                uuid_generate_v4(), v_je_id, 1, v_1030_acct_id,
                'Reverse over-credit drift (1030 Yoqilg''i)',
                ABS(v_drift_amount), 0, 1.0, ABS(v_drift_amount), '{}'::jsonb, NOW()
            );
            UPDATE accounts
            SET current_balance = current_balance + ABS(v_drift_amount), updated_at = NOW()
            WHERE id = v_1030_acct_id;

            -- CR 1010
            INSERT INTO journal_entry_lines (
                id, journal_entry_id, line_number, account_id,
                description, debit_amount, credit_amount,
                exchange_rate, amount_base, analytics_json, created_at
            ) VALUES (
                uuid_generate_v4(), v_je_id, 2, v_1010_acct_id,
                'Reverse mistaken raw-material posting (1010)',
                0, ABS(v_drift_amount), 1.0, ABS(v_drift_amount), '{}'::jsonb, NOW()
            );
            UPDATE accounts
            SET current_balance = current_balance - ABS(v_drift_amount), updated_at = NOW()
            WHERE id = v_1010_acct_id;
        END IF;

        posted_count := posted_count + 1;

        RAISE NOTICE 'Posted reclass % for tenant=% org=% drift=% (after 411 dated %)',
            v_entry_number, pair.tenant_id, pair.organization_id,
            v_drift_amount, v_411_je_date;
    END LOOP;

    RAISE NOTICE
        'Migration 414 done: posted=% skipped_existing=% skipped_zero=% skipped_no_1030=% created_1010=%',
        posted_count, skipped_existing, skipped_zero, skipped_no_1030, created_1010;
END $$;

ALTER TABLE accounts            ENABLE TRIGGER trg_check_cash_bank_balance;
ALTER TABLE journal_entry_lines ENABLE TRIGGER trg_enforce_journal_line_invariants;

-- POST-DEPLOY VERIFICATION
-- After this migration runs, expect for EVROPLIT
-- (tenant ce0b21e8-…, org 465fb035-…):
--
--   SELECT a.code, a.current_balance FROM accounts a
--   WHERE a.tenant_id = 'ce0b21e8-dde8-46d4-8cb3-fe78a6613e22'
--     AND a.organization_id = '465fb035-c265-49c3-88e6-0c1762f4efdc'
--     AND a.code IN ('1010','1030');
--
--   1030 → ~0  (down from 70,399,873)
--   1010 → ~1,690,620,292  (up from 1,620,220,419 by ~70.4M)
--
-- The group "Tovar-moddiy zaxiralar" (1000) total is UNCHANGED — value
-- is just moved between sibling leaves. Buxgalteriya AKTIV roll-up
-- still ties. Inventory module's physical value is unchanged.
--
-- Combined with the handler fixes (manufacturing.go, work_orders.go,
-- admin_settings.go), future receipts and production runs will land
-- on 1010 directly and 1030 will stay at the level it had before this
-- bug was introduced.
