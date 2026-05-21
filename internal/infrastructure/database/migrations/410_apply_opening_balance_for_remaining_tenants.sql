-- 410_apply_opening_balance_for_remaining_tenants.sql
--
-- Posts the inventory opening-balance JE for every (tenant, organization)
-- pair that:
--   (a) has physical inventory worth more than zero AT cost, and
--   (b) does NOT already have an INV-OPENING-* journal entry from
--       migration 405 / 410 (idempotency).
--
-- WHY THIS EXISTS
-- Migration 405 used `WHERE a.code IN ('3010','3000','3110')` to find the
-- credit-side equity account. In the standard NAS chart of accounts those
-- codes are NOT equity — `3xxx` is deferred expenses / receivables and
-- equity sits at `8xxx` (8310 Oddiy aksiyalar, 8400 Zaxira kapitali,
-- 8500 Qo'shimcha kapital, 8600 Maqsadli tushumlar, 8710 Taqsimlanmagan
-- foyda, 8720 E'lon qilingan dividendlar).
--
-- For tenants whose chart happened to have a leaf at 3110 (the buggy local
-- dev tenant), 405 posted to the wrong account and migration 406 re-routed
-- it to 8710. But for tenants whose chart only has 8xxx for equity (e.g.,
-- EVROPLIT in production), 405 found no matching code at all, logged
-- "No equity leaf in tenant=... org=..." and silently skipped them.
-- Result: their inventory module shows 888M of physical stock, but the
-- GL has no opening-balance recognition, so:
--   * Asset accounts (1030 Xom ashyo etc.) reflect only purchase activity
--     post-opening — typically a fraction of the true inventory value.
--   * KAPITAL stays at 0 because no equity was credited.
--   * The dashboard's "Jami qiymat" (inventory module) and Buxgalteriya's
--     AKTIV (GL roll-up) disagree by hundreds of millions.
--
-- WHAT THIS MIGRATION DOES
-- Identical math to 405, but with two corrections:
--   1. Equity destination is selected structurally via
--      `account_types.category = 'equity'` (not by code), preferring
--      8710 → 8500 → 8400 → 8310 → 8600 → 8720. Works for every tenant
--      regardless of code scheme.
--   2. Inventory destination is also selected structurally
--      (`category = 'asset'` + a code-preference list under 1xxx /
--      2xxx) so we don't repeat the 405 bug in reverse.
--
-- IDEMPOTENCY
-- Each posting is tagged with reference =
-- 'INV-OPENING-<tenant>-<org>'. If a previous run (405 or this one)
-- already posted such an entry, it's skipped. So 410 is safe to re-run,
-- and tenants that 405 handled correctly will not be re-processed.
--
-- TRIGGER BYPASS (same pattern as 404/405/406/407/408/409)
-- Disable the leaf-check and cash/bank-balance triggers for this
-- transaction only. PostgreSQL's transactional DDL restores them on
-- COMMIT (success or rollback). No trigger function is altered.

ALTER TABLE journal_entry_lines DISABLE TRIGGER trg_enforce_journal_line_invariants;
ALTER TABLE accounts            DISABLE TRIGGER trg_check_cash_bank_balance;

DO $$
DECLARE
    pair RECORD;
    v_inv_value       NUMERIC;
    v_inv_acct_id     UUID;
    v_inv_acct_code   TEXT;
    v_equity_acct_id  UUID;
    v_equity_acct_code TEXT;
    v_journal_id      UUID;
    v_next_seq        INT;
    v_je_id           UUID;
    v_entry_number    TEXT;
    v_reference       TEXT;
    posted_count      INT := 0;
    skipped_existing  INT := 0;
    skipped_no_acct   INT := 0;
    skipped_no_inv    INT := 0;
BEGIN
    FOR pair IN
        SELECT DISTINCT i.tenant_id, w.organization_id
        FROM inventory i
        JOIN warehouses w ON w.id = i.warehouse_id
        JOIN products p   ON p.id = i.product_id AND p.deleted_at IS NULL
        WHERE w.organization_id IS NOT NULL
          AND COALESCE(w.warehouse_type, 'regular') <> 'scrap'
          AND i.quantity_on_hand > 0
    LOOP
        v_reference := 'INV-OPENING-' || pair.tenant_id::text || '-' || pair.organization_id::text;

        -- Idempotency: 405 may already have posted for this pair
        IF EXISTS (
            SELECT 1 FROM journal_entries
            WHERE reference = v_reference
              AND deleted_at IS NULL
        ) THEN
            skipped_existing := skipped_existing + 1;
            CONTINUE;
        END IF;

        -- Compute inventory value (same formula as 405)
        SELECT COALESCE(
            SUM(i.quantity_on_hand * COALESCE(NULLIF(i.unit_cost, 0), p.cost_price, 0)),
            0
        )
        INTO v_inv_value
        FROM inventory i
        JOIN warehouses w ON w.id = i.warehouse_id
                          AND w.organization_id = pair.organization_id
        JOIN products p   ON p.id = i.product_id AND p.deleted_at IS NULL
        WHERE i.tenant_id = pair.tenant_id
          AND COALESCE(w.warehouse_type, 'regular') <> 'scrap'
          AND i.quantity_on_hand > 0;

        IF v_inv_value <= 0 THEN
            skipped_no_inv := skipped_no_inv + 1;
            CONTINUE;
        END IF;

        -- Inventory leaf — structural (category=asset) + code preference.
        -- Codes commonly used for stock accounts under NAS / similar charts:
        --   1030 Xom ashyo (raw materials)
        --   2810 Tayyor mahsulot (finished goods)
        --   2910 Tovarlar sotish uchun (goods for sale)
        --   1020 Sotib olingan yarim tayyor (purchased semi-finished)
        --   1040 Idish va idish materiallari (packaging)
        --   1050 Ehtiyot qismlar (spare parts)
        --   2010 Tugallanmagan ishlab chiqarish (WIP)
        SELECT a.id, a.code
        INTO v_inv_acct_id, v_inv_acct_code
        FROM accounts a
        JOIN account_types at ON at.id = a.account_type_id
        WHERE a.tenant_id = pair.tenant_id
          AND a.organization_id = pair.organization_id
          AND a.deleted_at IS NULL
          AND COALESCE(a.is_leaf, true) = true
          AND at.category = 'asset'
          AND a.code IN ('1030','2810','2910','1020','1040','1050','2010')
        ORDER BY CASE a.code
            WHEN '1030' THEN 1
            WHEN '2910' THEN 2
            WHEN '2810' THEN 3
            WHEN '1020' THEN 4
            WHEN '1040' THEN 5
            WHEN '1050' THEN 6
            WHEN '2010' THEN 7
            ELSE 99
        END
        LIMIT 1;

        IF v_inv_acct_id IS NULL THEN
            RAISE WARNING 'No inventory leaf found in tenant=% org=%; skipping',
                pair.tenant_id, pair.organization_id;
            skipped_no_acct := skipped_no_acct + 1;
            CONTINUE;
        END IF;

        -- Equity leaf — structural (category=equity), code preference for the
        -- typical NAS layout. Migration 405's bug was hard-coding 3xxx here;
        -- we use the structural filter so any tenant's chart works.
        SELECT a.id, a.code
        INTO v_equity_acct_id, v_equity_acct_code
        FROM accounts a
        JOIN account_types at ON at.id = a.account_type_id
        WHERE a.tenant_id = pair.tenant_id
          AND a.organization_id = pair.organization_id
          AND a.deleted_at IS NULL
          AND COALESCE(a.is_leaf, true) = true
          AND at.category = 'equity'
        ORDER BY CASE a.code
            WHEN '8710' THEN 1   -- Retained earnings prior years (textbook target)
            WHEN '8500' THEN 2   -- Additional paid-in capital
            WHEN '8400' THEN 3   -- Reserve capital
            WHEN '8310' THEN 4   -- Common shares
            WHEN '8600' THEN 5   -- Targeted income
            WHEN '8720' THEN 6   -- Declared dividends (last resort)
            ELSE 99
        END,
        a.code
        LIMIT 1;

        IF v_equity_acct_id IS NULL THEN
            RAISE WARNING 'No equity leaf found in tenant=% org=%; skipping (chart has no category=equity leaves)',
                pair.tenant_id, pair.organization_id;
            skipped_no_acct := skipped_no_acct + 1;
            CONTINUE;
        END IF;

        -- Journal — prefer MISC/GEN/GENERAL/STOCK in that order
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
            RAISE WARNING 'No journal found for tenant=%; skipping', pair.tenant_id;
            skipped_no_acct := skipped_no_acct + 1;
            CONTINUE;
        END IF;

        v_je_id := uuid_generate_v4();
        v_entry_number := 'OPEN' || LPAD(v_next_seq::text, 6, '0');

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
            'Inventory opening balance — align GL with inventory module value. ' ||
                'Posted by migration 410 to fix tenants migration 405 silently ' ||
                'skipped due to a hardcoded 3xxx equity-code lookup that did not ' ||
                'match the standard NAS 8xxx equity codes. See migration file.',
            'opening_balance',
            NULL,
            1.0,
            v_inv_value,
            v_inv_value,
            'posted',
            NOW(),
            NOW()
        );

        UPDATE journals SET next_number = next_number + 1, updated_at = NOW()
        WHERE id = v_journal_id;

        -- DR inventory leaf
        INSERT INTO journal_entry_lines (
            id, journal_entry_id, line_number, account_id,
            description, debit_amount, credit_amount,
            exchange_rate, amount_base, analytics_json, created_at
        ) VALUES (
            uuid_generate_v4(), v_je_id, 1, v_inv_acct_id,
            'Inventory opening balance (' || v_inv_acct_code || ')',
            v_inv_value, 0, 1.0, v_inv_value, '{}'::jsonb, NOW()
        );

        UPDATE accounts
        SET current_balance = current_balance + v_inv_value, updated_at = NOW()
        WHERE id = v_inv_acct_id;

        -- CR equity leaf (the correct one this time)
        INSERT INTO journal_entry_lines (
            id, journal_entry_id, line_number, account_id,
            description, debit_amount, credit_amount,
            exchange_rate, amount_base, analytics_json, created_at
        ) VALUES (
            uuid_generate_v4(), v_je_id, 2, v_equity_acct_id,
            'Opening capital — inventory recognition (' || v_equity_acct_code || ')',
            0, v_inv_value, 1.0, v_inv_value, '{}'::jsonb, NOW()
        );

        UPDATE accounts
        SET current_balance = current_balance - v_inv_value, updated_at = NOW()
        WHERE id = v_equity_acct_id;

        posted_count := posted_count + 1;

        RAISE NOTICE 'Posted opening JE % for tenant=% org=% amount=% (DR %, CR %)',
            v_entry_number, pair.tenant_id, pair.organization_id,
            v_inv_value, v_inv_acct_code, v_equity_acct_code;
    END LOOP;

    RAISE NOTICE 'Migration 410 done: posted=% skipped_existing=% skipped_no_inv=% skipped_no_acct=%',
        posted_count, skipped_existing, skipped_no_inv, skipped_no_acct;
END $$;

ALTER TABLE accounts            ENABLE TRIGGER trg_check_cash_bank_balance;
ALTER TABLE journal_entry_lines ENABLE TRIGGER trg_enforce_journal_line_invariants;

-- POST-DEPLOY VERIFICATION
-- After running, every (tenant, org) with non-zero inventory should have:
--   * An INV-OPENING-<tenant>-<org> JE in journal_entries
--   * Its inventory leaf account (1030 or fallback) bumped by the inventory value
--   * Its equity leaf (8710 or fallback) credited by the same amount
--
--   SELECT je.organization_id, je.entry_number, je.reference,
--          je.total_debit, je.entry_date::date
--   FROM journal_entries je
--   WHERE je.reference LIKE 'INV-OPENING-%'
--     AND je.deleted_at IS NULL
--   ORDER BY je.organization_id, je.entry_date;
--
-- For each org, the dashboard's "Jami qiymat" (inventory module value)
-- should now equal the sum of inventory-leaf current_balances, and
-- Buxgalteriya's KAPITAL card should reflect the equity recognition.
