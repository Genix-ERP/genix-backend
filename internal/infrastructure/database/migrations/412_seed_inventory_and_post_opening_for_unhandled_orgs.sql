-- 412_seed_inventory_and_post_opening_for_unhandled_orgs.sql
--
-- Migration 410 walked every (tenant, org) pair with non-zero physical
-- inventory and tried to post an opening-balance JE. It silently
-- skipped any pair that didn't have:
--   (a) at least one inventory-like asset leaf in its chart, OR
--   (b) at least one equity leaf, OR
--   (c) at least one journal of type MISC/GEN/GENERAL/STOCK.
--
-- Production diagnosis revealed three orgs under tenant "Yuksalish
-- Demo" where the chart of accounts was provisioned without ANY
-- inventory accounts:
--
--     ELPARVAR STROY HOUSE 777" MCHJ  — physical 32,600,454
--     MCHJ JAVOHIR AVENUE             — physical  1,998,000
--     NASAF AVENUE MCHJ               — physical    186,000
--
-- For these orgs `has_opening_je = false` and `gl_accts = 0`. Their
-- inventory module shows real stock, but Buxgalteriya AKTIV shows
-- nothing on the asset side from these orgs — a clean
-- mis-classification rather than a math gap.
--
-- WHAT THIS MIGRATION DOES
-- Same job as 410, but self-healing about missing dependencies. For
-- every (tenant, org) with physical inventory > 0 and no existing
-- INV-OPENING-* JE:
--
--   1. Ensure account 1010 (Xom ashyo va materiallar) exists.
--      Create it under the INV account_type if missing.
--   2. Ensure at least one equity leaf exists.
--      Pick by NAS code priority (8710 → 8500 → 8400 → 8310 → 8600 →
--      8720). If none exist, create 8710 (Taqsimlanmagan foyda /
--      Retained Earnings prior years) under the RETAIN account_type.
--   3. Ensure a posting journal exists. Prefer MISC/GEN/GENERAL/STOCK.
--      If none, create MISC.
--   4. Compute physical inventory value with the same formula 410
--      and the frontend `getInventorySummary` use:
--          SUM(qty_on_hand × COALESCE(NULLIF(unit_cost, 0),
--                                     product.cost_price, 0))
--   5. Post the opening JE: DR 1010, CR equity. Tag with
--      'INV-OPENING-<tenant>-<org>' — IDENTICAL reference to 410, so
--      this migration is fully idempotent with 410 and 411.
--
-- WHY THIS IS SAFE
--   * Read-only on existing JEs — nothing already booked is modified.
--   * The opening reference matches 410, so re-running either is a
--     no-op for pairs already handled.
--   * Account/journal creation is gated on UNIQUE(tenant, code), so
--     re-runs don't duplicate.
--   * 411 only acts on INV-OPENING JEs whose DR line landed on 1030.
--     The JEs this migration posts go directly to 1010, so they are
--     out of scope for 411 and won't be reclassified.
--
-- TRIGGER BYPASS (same pattern as 404–411)

ALTER TABLE journal_entry_lines DISABLE TRIGGER trg_enforce_journal_line_invariants;
ALTER TABLE accounts            DISABLE TRIGGER trg_check_cash_bank_balance;

DO $$
DECLARE
    pair                RECORD;
    v_inv_value         NUMERIC;
    v_inv_acct_id       UUID;
    v_inv_acct_code     TEXT := '1010';
    v_inv_acct_name     TEXT := 'Xom ashyo va materiallar';
    v_inv_type_id       UUID;
    v_equity_acct_id    UUID;
    v_equity_acct_code  TEXT;
    v_equity_acct_name  TEXT;
    v_retain_type_id    UUID;
    v_journal_id        UUID;
    v_journal_code      TEXT;
    v_next_seq          INT;
    v_je_id             UUID;
    v_entry_number      TEXT;
    v_reference         TEXT;
    posted_count        INT := 0;
    skipped_existing    INT := 0;
    skipped_no_inv      INT := 0;
    created_1010        INT := 0;
    created_8710        INT := 0;
    created_misc        INT := 0;
BEGIN
    -- Global account-type IDs (seeded once in migration 002). Used
    -- whenever we have to create a leaf account.
    SELECT id INTO v_inv_type_id    FROM account_types WHERE code = 'INV'    LIMIT 1;
    SELECT id INTO v_retain_type_id FROM account_types WHERE code = 'RETAIN' LIMIT 1;

    IF v_inv_type_id IS NULL OR v_retain_type_id IS NULL THEN
        RAISE EXCEPTION 'account_types INV or RETAIN not found — migration 002 seed missing?';
    END IF;

    FOR pair IN
        -- Every (tenant, org) that owns physical inventory.
        SELECT DISTINCT i.tenant_id, w.organization_id
        FROM inventory i
        JOIN warehouses w ON w.id = i.warehouse_id
        JOIN products   p ON p.id = i.product_id AND p.deleted_at IS NULL
        WHERE w.organization_id IS NOT NULL
          AND COALESCE(w.warehouse_type, 'regular') <> 'scrap'
          AND i.quantity_on_hand > 0
    LOOP
        v_reference := 'INV-OPENING-'
                    || pair.tenant_id::text
                    || '-' || pair.organization_id::text;

        -- Idempotency: skip pairs that 405 / 410 (or a prior run of
        -- 412) already handled. Same reference contract.
        IF EXISTS (
            SELECT 1 FROM journal_entries
            WHERE reference = v_reference
              AND deleted_at IS NULL
        ) THEN
            skipped_existing := skipped_existing + 1;
            CONTINUE;
        END IF;

        -- Compute physical inventory value (same formula as 410 / 411
        -- comments). Recomputed at migration time so we use today's
        -- actual stock, not whatever was true when 410 ran.
        SELECT COALESCE(
            SUM(i.quantity_on_hand * COALESCE(NULLIF(i.unit_cost, 0), p.cost_price, 0)),
            0
        )
        INTO v_inv_value
        FROM inventory i
        JOIN warehouses w ON w.id = i.warehouse_id
                          AND w.organization_id = pair.organization_id
        JOIN products   p ON p.id = i.product_id AND p.deleted_at IS NULL
        WHERE i.tenant_id = pair.tenant_id
          AND COALESCE(w.warehouse_type, 'regular') <> 'scrap'
          AND i.quantity_on_hand > 0;

        IF v_inv_value <= 0 THEN
            skipped_no_inv := skipped_no_inv + 1;
            CONTINUE;
        END IF;

        -- ============================================================
        -- 1. Ensure 1010 Xom ashyo va materiallar exists
        -- ============================================================
        SELECT id INTO v_inv_acct_id
        FROM accounts
        WHERE tenant_id = pair.tenant_id
          AND organization_id = pair.organization_id
          AND code = v_inv_acct_code
          AND deleted_at IS NULL
        LIMIT 1;

        IF v_inv_acct_id IS NULL THEN
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
                v_inv_acct_code,
                v_inv_acct_name,
                'Auto-created by migration 412. Org''s chart of accounts '
                  || 'had no inventory leaf, so 410 silently skipped posting '
                  || 'the inventory opening balance for this org. Created '
                  || 'here so the opening JE has a real account to land on.',
                true,
                0,
                0,
                NOW(),
                NOW()
            )
            RETURNING id INTO v_inv_acct_id;
            created_1010 := created_1010 + 1;
        END IF;

        -- ============================================================
        -- 2. Ensure an equity leaf exists. Prefer 8710 Taqsimlanmagan
        -- foyda; fall back through other 8xxx codes; create 8710 if
        -- the chart has no equity leaf at all.
        -- ============================================================
        SELECT a.id, a.code, a.name
        INTO v_equity_acct_id, v_equity_acct_code, v_equity_acct_name
        FROM accounts a
        JOIN account_types at ON at.id = a.account_type_id
        WHERE a.tenant_id = pair.tenant_id
          AND a.organization_id = pair.organization_id
          AND a.deleted_at IS NULL
          AND COALESCE(a.is_leaf, true) = true
          AND at.category = 'equity'
        ORDER BY CASE a.code
            WHEN '8710' THEN 1
            WHEN '8500' THEN 2
            WHEN '8400' THEN 3
            WHEN '8310' THEN 4
            WHEN '8600' THEN 5
            WHEN '8720' THEN 6
            ELSE 99
        END,
        a.code
        LIMIT 1;

        IF v_equity_acct_id IS NULL THEN
            v_equity_acct_code := '8710';
            v_equity_acct_name := 'Taqsimlanmagan foyda';

            -- It's possible (rare) that 8710 exists but isn't tagged
            -- as equity, or exists as a non-leaf. Use ON CONFLICT to
            -- be safe given UNIQUE(tenant_id, org_id, code).
            INSERT INTO accounts (
                id, tenant_id, organization_id, account_type_id,
                code, name, description,
                is_active, opening_balance, current_balance,
                created_at, updated_at
            ) VALUES (
                uuid_generate_v4(),
                pair.tenant_id,
                pair.organization_id,
                v_retain_type_id,
                v_equity_acct_code,
                v_equity_acct_name,
                'Auto-created by migration 412. Org had no equity leaf in '
                  || 'its chart of accounts, so the inventory opening JE '
                  || 'had no credit-side target. Created as Retained '
                  || 'Earnings (NAS code 8710).',
                true,
                0,
                0,
                NOW(),
                NOW()
            )
            ON CONFLICT (tenant_id, organization_id, code) DO NOTHING
            RETURNING id INTO v_equity_acct_id;

            -- If ON CONFLICT fired, re-read the existing row's id.
            IF v_equity_acct_id IS NULL THEN
                SELECT id INTO v_equity_acct_id
                FROM accounts
                WHERE tenant_id = pair.tenant_id
                  AND organization_id = pair.organization_id
                  AND code = v_equity_acct_code
                  AND deleted_at IS NULL
                LIMIT 1;
            ELSE
                created_8710 := created_8710 + 1;
            END IF;

            IF v_equity_acct_id IS NULL THEN
                RAISE WARNING 'Could not create or find equity leaf for tenant=% org=%; skipping',
                    pair.tenant_id, pair.organization_id;
                CONTINUE;
            END IF;
        END IF;

        -- ============================================================
        -- 3. Ensure a journal exists. Prefer MISC/GEN/GENERAL/STOCK;
        -- create MISC if none.
        -- ============================================================
        SELECT j.id, j.code, COALESCE(j.next_number, 1)
        INTO v_journal_id, v_journal_code, v_next_seq
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
            -- journals is UNIQUE(tenant_id, code), so any code we pick
            -- must be globally unique within the tenant. MISC is the
            -- least-conflicting choice.
            INSERT INTO journals (
                id, tenant_id, organization_id, code, name, type,
                is_active, next_number, created_at, updated_at
            ) VALUES (
                uuid_generate_v4(),
                pair.tenant_id,
                pair.organization_id,
                'MISC',
                'Boshqa operatsiyalar jurnali',
                'general',
                true,
                1,
                NOW(),
                NOW()
            )
            ON CONFLICT (tenant_id, code) DO NOTHING
            RETURNING id, code INTO v_journal_id, v_journal_code;

            IF v_journal_id IS NULL THEN
                SELECT j.id, j.code, COALESCE(j.next_number, 1)
                INTO v_journal_id, v_journal_code, v_next_seq
                FROM journals j
                WHERE j.tenant_id = pair.tenant_id
                  AND j.code = 'MISC'
                  AND j.deleted_at IS NULL
                LIMIT 1;
            ELSE
                v_next_seq := 1;
                created_misc := created_misc + 1;
            END IF;

            IF v_journal_id IS NULL THEN
                RAISE WARNING 'Could not create or find a journal for tenant=%; skipping',
                    pair.tenant_id;
                CONTINUE;
            END IF;
        END IF;

        -- ============================================================
        -- 4. Post the opening JE: DR 1010, CR equity.
        -- ============================================================
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
            'Inventory opening balance — align GL with inventory module value. '
              || 'Posted by migration 412 for orgs whose chart of accounts was '
              || 'missing the inventory and/or equity leaves required by 410. '
              || 'Missing accounts were auto-created and the opening JE posted '
              || 'against them. See migration file header for details.',
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

        -- DR 1010 Xom ashyo
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

        -- CR equity
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

        RAISE NOTICE 'Posted opening JE % for tenant=% org=% amount=% (DR % via %, CR % via %)',
            v_entry_number, pair.tenant_id, pair.organization_id,
            v_inv_value, v_inv_acct_code, v_journal_code,
            v_equity_acct_code, v_journal_code;
    END LOOP;

    RAISE NOTICE
        'Migration 412 done: posted=% skipped_existing=% skipped_no_inv=% created_1010=% created_8710=% created_misc=%',
        posted_count, skipped_existing, skipped_no_inv,
        created_1010, created_8710, created_misc;
END $$;

ALTER TABLE accounts            ENABLE TRIGGER trg_check_cash_bank_balance;
ALTER TABLE journal_entry_lines ENABLE TRIGGER trg_enforce_journal_line_invariants;

-- POST-DEPLOY VERIFICATION
-- After this migration runs on production, expect (using the diagnostic
-- queries from the chat):
--
--   ELPARVAR STROY HOUSE 777" MCHJ
--     physical=32,600,454  gl=32,600,454  diff=0  has_opening_je=true
--     (1010 auto-created, opening JE posted)
--
--   MCHJ JAVOHIR AVENUE
--     physical=1,998,000  gl=1,998,000  diff=0  has_opening_je=true
--
--   NASAF AVENUE MCHJ
--     physical=186,000  gl=186,000  diff=0  has_opening_je=true
--
-- All three should now show in Buxgalteriya AKTIV with proper Xom ashyo
-- balance, and KAPITAL credited with the corresponding opening amount.
--
-- The 412 migration is paired with 411 and the original 410:
--   * 410 — original opening for orgs that had all 3 dependencies
--   * 411 — reclass orgs where 410 landed opening on 1030 (Fuel)
--   * 412 — bootstrap missing accounts and post opening for orgs 410 skipped
--
-- After all three are applied, every (tenant, org) with physical
-- inventory > 0 will have an INV-OPENING JE on 1010 Xom ashyo, no
-- inventory value mis-classified as Fuel, and GL inventory sum >=
-- physical inventory value (residual being only the consumption/COGS
-- posting gap, tracked separately).
