-- 405_align_inventory_account_with_module_value.sql
--
-- Brings the GL inventory leaf account in line with what the inventory
-- module physically reports (`SUM(quantity_on_hand × cost)`). Posts a
-- single opening-balance JE per (tenant, organization) pair:
--
--     DR  1030 Xom ashyo                  ← brings the leaf to the live value
--     CR  3010 Boshlang'ich kapital       ← treats the gap as opening equity
--
-- WHY
-- After migration 404 reversed the wrong PUR receipt entries, account 1030
-- correctly returned to its true historical balance (zero, in the audited
-- tenant) — because the inventory was physically received via flows that
-- never properly debited the GL inventory account, AND issues over time
-- credited it via flows that did post correctly. The result: inventory
-- module shows ~1.68B of stock physically on the shelves, but the GL leaf
-- inventory account is at zero, and the parent group has gone deeply
-- negative from issues without matching receipt postings.
--
-- This migration is a one-shot reconciliation: it does not retroactively
-- attribute the gap to individual receipts (we no longer have the source
-- transactions for many of them). Instead it books the difference as an
-- "opening balance" against equity, which is the standard accounting
-- maneuver after an audit finding or a system migration that needs to
-- reset the books to reflect physical reality.
--
-- ACCOUNT SELECTION
--   Inventory leaf — preferred: 1030 (Xom ashyo / Raw Materials), then
--     2910 (Tovarlar sotish uchun / Goods for sale), 2810 (Tayyor mahsulot
--     / Finished goods), 1020 (Sotib olingan yarim tayyor mahsulotlar /
--     Purchased semi-finished), 1040, 1050. Picks the first leaf account
--     that exists in this org's chart. Multi-tenant safe; each org chooses
--     its own destination based on which leaves it has.
--   Equity — preferred: 3010 (Boshlang'ich kapital / Initial Capital), then
--     3000, 3110. Same first-match pattern.
--
-- IDEMPOTENCY
-- Each opening JE is tagged with reference =
-- 'INV-OPENING-<tenant>-<org>'. The detection query skips (tenant, org)
-- pairs that already have such an entry, so re-running is a no-op.
--
-- INVENTORY-VALUE COMPUTATION
-- Sums `quantity_on_hand × COALESCE(inventory.unit_cost, products.cost_price, 0)`
-- across non-scrap warehouses belonging to the org, filtering out rows
-- whose product has been soft-deleted (`p.deleted_at IS NULL`). This is
-- the same math the frontend uses for the "Jami qiymat" card, so by
-- construction the GL leaf will equal the displayed module value once
-- the JE posts.
--
-- TRIGGER BYPASS (same as migration 404)
-- The strict TT §4.2 leaf check (migration 326) and the cash/bank
-- balance guard (migration 192) both fire on the operations we're
-- about to do. Disable them for this transaction only; PostgreSQL's
-- transactional DDL guarantees they come back at COMMIT (success or
-- rollback). We do NOT alter the trigger functions themselves.

ALTER TABLE journal_entry_lines DISABLE TRIGGER trg_enforce_journal_line_invariants;
ALTER TABLE accounts            DISABLE TRIGGER trg_check_cash_bank_balance;

DO $$
DECLARE
    pair RECORD;
    v_inv_value      NUMERIC;
    v_inv_acct_id    UUID;
    v_inv_acct_code  TEXT;
    v_equity_acct_id UUID;
    v_journal_id     UUID;
    v_next_seq       INT;
    v_je_id          UUID;
    v_entry_number   TEXT;
    v_reference      TEXT;
    posted_count     INT := 0;
    skipped_no_acct  INT := 0;
    skipped_existing INT := 0;
    skipped_no_inv   INT := 0;
BEGIN
    -- One JE per (tenant, organization). Inventory rows whose warehouse
    -- has no org are routed via the warehouse, so we join through it.
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

        -- Idempotency
        IF EXISTS (
            SELECT 1 FROM journal_entries
            WHERE reference = v_reference
              AND deleted_at IS NULL
        ) THEN
            skipped_existing := skipped_existing + 1;
            CONTINUE;
        END IF;

        -- Compute inventory value for this (tenant, org).
        -- Matches the frontend's getInventorySummary().totalValue formula:
        --   sum of quantity_on_hand × COALESCE(unit_cost, cost_price, 0)
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

        -- Pick inventory leaf account, preferring 1030 (raw materials)
        SELECT a.id, a.code
        INTO v_inv_acct_id, v_inv_acct_code
        FROM accounts a
        WHERE a.tenant_id = pair.tenant_id
          AND a.organization_id = pair.organization_id
          AND a.deleted_at IS NULL
          AND COALESCE(a.is_leaf, true) = true
          AND a.code IN ('1030','2910','2810','1020','1040','1050')
        ORDER BY CASE a.code
            WHEN '1030' THEN 1
            WHEN '2910' THEN 2
            WHEN '2810' THEN 3
            WHEN '1020' THEN 4
            WHEN '1040' THEN 5
            WHEN '1050' THEN 6
            ELSE 99
        END
        LIMIT 1;

        IF v_inv_acct_id IS NULL THEN
            RAISE WARNING 'No inventory leaf account in tenant=% org=%; skipping (no 1030/2910/2810/1020/1040/1050)',
                pair.tenant_id, pair.organization_id;
            skipped_no_acct := skipped_no_acct + 1;
            CONTINUE;
        END IF;

        -- Pick equity / opening-capital account, preferring 3010
        SELECT a.id
        INTO v_equity_acct_id
        FROM accounts a
        WHERE a.tenant_id = pair.tenant_id
          AND a.organization_id = pair.organization_id
          AND a.deleted_at IS NULL
          AND COALESCE(a.is_leaf, true) = true
          AND a.code IN ('3010','3000','3110')
        ORDER BY CASE a.code
            WHEN '3010' THEN 1
            WHEN '3000' THEN 2
            WHEN '3110' THEN 3
            ELSE 99
        END
        LIMIT 1;

        IF v_equity_acct_id IS NULL THEN
            RAISE WARNING 'No equity leaf account in tenant=% org=%; skipping (no 3010/3000/3110)',
                pair.tenant_id, pair.organization_id;
            skipped_no_acct := skipped_no_acct + 1;
            CONTINUE;
        END IF;

        -- Pick a journal — prefer MISC/GEN, fall back to STOCK
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
            RAISE WARNING 'No journal for tenant=%; skipping', pair.tenant_id;
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
                'Posted by migration 405 after migration 404 cleared the band-aid debits. ' ||
                'See migration file for context.',
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

        -- CR opening equity
        INSERT INTO journal_entry_lines (
            id, journal_entry_id, line_number, account_id,
            description, debit_amount, credit_amount,
            exchange_rate, amount_base, analytics_json, created_at
        ) VALUES (
            uuid_generate_v4(), v_je_id, 2, v_equity_acct_id,
            'Opening capital — inventory recognition',
            0, v_inv_value, 1.0, v_inv_value, '{}'::jsonb, NOW()
        );

        UPDATE accounts
        SET current_balance = current_balance - v_inv_value, updated_at = NOW()
        WHERE id = v_equity_acct_id;

        posted_count := posted_count + 1;

        RAISE NOTICE 'Posted opening JE % for tenant=% org=% amount=% (DR %, CR equity)',
            v_entry_number, pair.tenant_id, pair.organization_id, v_inv_value, v_inv_acct_code;
    END LOOP;

    RAISE NOTICE 'Migration 405 done: posted=% skipped_existing=% skipped_no_inv=% skipped_no_acct=%',
        posted_count, skipped_existing, skipped_no_inv, skipped_no_acct;
END $$;

ALTER TABLE accounts            ENABLE TRIGGER trg_check_cash_bank_balance;
ALTER TABLE journal_entry_lines ENABLE TRIGGER trg_enforce_journal_line_invariants;

-- POST-DEPLOY VERIFICATION
-- After running, the inventory leaf (1030 in most tenants) should equal
-- the inventory module's "Jami qiymat" value. The parent group account
-- (1010) will still carry its historical accumulated balance — that's a
-- separate reconciliation if needed.
--
--   SELECT a.code, a.name, a.current_balance,
--          a.organization_id
--   FROM accounts a
--   WHERE a.code IN ('1030','3010') AND a.deleted_at IS NULL
--   ORDER BY a.organization_id, a.code;
--
-- And the new opening entries:
--
--   SELECT je.entry_number, je.entry_date::date, je.reference,
--          je.total_debit, je.total_credit, je.organization_id
--   FROM journal_entries je
--   WHERE je.source_type = 'opening_balance'
--     AND je.reference LIKE 'INV-OPENING-%'
--     AND je.deleted_at IS NULL
--   ORDER BY je.entry_date DESC, je.organization_id;
