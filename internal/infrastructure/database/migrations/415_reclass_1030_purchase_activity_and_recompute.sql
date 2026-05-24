-- 415_reclass_1030_purchase_activity_and_recompute.sql
--
-- Why this migration exists
-- -------------------------
-- Migration 414 attempted to reclass post-411 Yoqilg'i (1030) drift to
-- Xom ashyo (1010) but filtered too narrowly (description patterns +
-- source_type IN production_order/work_order/purchase_receipt). The
-- *real* source for EVROPLIT and similar tenants was `purchase_invoice`
-- with descriptions like 'Vendor Bill BILL-...' — 24 raw-material
-- bills posted between May 14–21 totalling ~156M, all routed to 1030
-- (Fuel) by getInventoryAccountByType(..., "raw") which used code 1030
-- as the fallback. The handler is now fixed (admin_settings.go), but
-- the historical bad postings are still on 1030.
--
-- Additionally: production diagnosis revealed a balance drift —
-- `accounts.current_balance` for 1030 (70.4M) did not match
-- SUM(jel.debit - jel.credit) on 1030 (156.3M). Some prior step
-- adjusted current_balance directly without matching JE lines.
-- Migration 407 recomputed once but additional drift accumulated
-- afterwards.
--
-- What this migration does (per tenant, org)
-- ------------------------------------------
--   1. Identify "bug-induced" 1030 lines: any JE line on 1030 except
--      the OPENING (source_type='opening_balance') and the 411 RECLASS
--      (reference LIKE 'INV-RECLASS-1030-TO-1010-%'). This is broader
--      than 414's source_type filter and is appropriate because real
--      fuel postings under these tenants are essentially nonexistent —
--      anything on 1030 that isn't the opening or its reverse is the
--      handler bug.
--   2. Sum DR-CR over those lines = misclassified net.
--   3. Ensure 1010 leaf exists for the org (auto-create like 412 if
--      missing).
--   4. Post one reclass JE per org: DR 1010 / CR 1030 for the
--      misclassified net (or the reverse if it's negative). Reference
--      'INV-RECLASS-1030-DRIFT-V2-<tenant>-<org>' — different suffix
--      from 414 so this migration is independently idempotent.
--   5. Original JE lines preserved (audit trail).
--   6. AFTER the loop, recompute current_balance for every 1010 and
--      1030 leaf in the system from SUM(debit-credit). This fixes the
--      ~86M balance drift on top of the reclass.
--
-- Safety
-- ------
--   * Same trigger-bypass pattern as 404-414 (transactional, restored
--     on commit).
--   * The reference uniqueness check makes re-runs no-ops.
--   * Recompute step ONLY touches 1010 and 1030 leaves; doesn't touch
--     other accounts whose drift may be tracked separately.
--   * Original lines stay; new reclass JE is appended.

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
    posted_count       INT := 0;
    skipped_existing   INT := 0;
    skipped_zero       INT := 0;
    skipped_no_1030    INT := 0;
    created_1010       INT := 0;
    recomputed_count   INT := 0;
BEGIN
    SELECT id INTO v_inv_type_id FROM account_types WHERE code = 'INV' LIMIT 1;
    IF v_inv_type_id IS NULL THEN
        RAISE EXCEPTION 'account_types code=INV missing — cannot ensure 1010 exists';
    END IF;

    -- =================================================================
    -- PHASE 1: reclassify 1030 → 1010 per (tenant, org).
    -- =================================================================
    FOR pair IN
        SELECT DISTINCT a.tenant_id, a.organization_id
        FROM accounts a
        WHERE a.code = '1030'
          AND a.deleted_at IS NULL
          AND a.organization_id IS NOT NULL
    LOOP
        v_reference := 'INV-RECLASS-1030-DRIFT-V2-'
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

        -- Sum the misclassified net: everything on 1030 EXCEPT the
        -- opening and the 411 reclass JE. Broader than 414's filter
        -- because real fuel transactions don't exist for these tenants
        -- and the runtime handler bug fed every raw-material flow
        -- through this account.
        SELECT COALESCE(SUM(jel.debit_amount - jel.credit_amount), 0)
        INTO v_drift_amount
        FROM journal_entry_lines jel
        JOIN journal_entries je ON je.id = jel.journal_entry_id
        WHERE jel.account_id = v_1030_acct_id
          AND je.tenant_id = pair.tenant_id
          AND je.organization_id = pair.organization_id
          AND je.deleted_at IS NULL
          AND COALESCE(je.source_type, '') <> 'opening_balance'
          AND COALESCE(je.reference, '') NOT LIKE 'INV-RECLASS-1030-TO-1010-%'
          AND COALESCE(je.reference, '') NOT LIKE 'INV-RECLASS-1030-DRIFT-%';

        IF v_drift_amount = 0 THEN
            skipped_zero := skipped_zero + 1;
            CONTINUE;
        END IF;

        -- Ensure 1010 exists (auto-create like 412).
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
                'Auto-created by migration 415. Org had no 1010 leaf so '
                  || 'the post-411 reclass had no target account.',
                true,
                0,
                0,
                NOW(),
                NOW()
            )
            ON CONFLICT (tenant_id, organization_id, code) DO NOTHING
            RETURNING id INTO v_1010_acct_id;

            IF v_1010_acct_id IS NULL THEN
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

        -- Pick a posting journal (same priority as 412).
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
            RAISE WARNING 'No posting journal for tenant=%; skipping', pair.tenant_id;
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
            'Reclass full 1030 Yoqilg''i activity (purchase invoices + '
              || 'production consumption) to 1010 Xom ashyo va materiallar. '
              || 'Corrects the runtime handler bug that routed raw-material '
              || 'purchases and consumption through the Fuel account. '
              || 'Migration 414 missed this because its filter only matched '
              || 'production_order/work_order/purchase_receipt sources — '
              || 'EVROPLIT''s activity is dominated by purchase_invoice. '
              || 'Original JE lines preserved.',
            'reclassification',
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
            INSERT INTO journal_entry_lines (
                id, journal_entry_id, line_number, account_id,
                description, debit_amount, credit_amount,
                exchange_rate, amount_base, analytics_json, created_at
            ) VALUES (
                uuid_generate_v4(), v_je_id, 1, v_1010_acct_id,
                'Reclass drift in (1010 Xom ashyo)',
                v_drift_amount, 0, 1.0, v_drift_amount, '{}'::jsonb, NOW()
            );
            INSERT INTO journal_entry_lines (
                id, journal_entry_id, line_number, account_id,
                description, debit_amount, credit_amount,
                exchange_rate, amount_base, analytics_json, created_at
            ) VALUES (
                uuid_generate_v4(), v_je_id, 2, v_1030_acct_id,
                'Reclass drift out (1030 Yoqilg''i)',
                0, v_drift_amount, 1.0, v_drift_amount, '{}'::jsonb, NOW()
            );
        ELSE
            INSERT INTO journal_entry_lines (
                id, journal_entry_id, line_number, account_id,
                description, debit_amount, credit_amount,
                exchange_rate, amount_base, analytics_json, created_at
            ) VALUES (
                uuid_generate_v4(), v_je_id, 1, v_1030_acct_id,
                'Reverse over-credit drift (1030 Yoqilg''i)',
                ABS(v_drift_amount), 0, 1.0, ABS(v_drift_amount), '{}'::jsonb, NOW()
            );
            INSERT INTO journal_entry_lines (
                id, journal_entry_id, line_number, account_id,
                description, debit_amount, credit_amount,
                exchange_rate, amount_base, analytics_json, created_at
            ) VALUES (
                uuid_generate_v4(), v_je_id, 2, v_1010_acct_id,
                'Reverse mistaken raw-material posting (1010)',
                0, ABS(v_drift_amount), 1.0, ABS(v_drift_amount), '{}'::jsonb, NOW()
            );
        END IF;

        posted_count := posted_count + 1;

        RAISE NOTICE 'Posted reclass % for tenant=% org=% drift=%',
            v_entry_number, pair.tenant_id, pair.organization_id, v_drift_amount;
    END LOOP;

    -- =================================================================
    -- PHASE 2: recompute current_balance for every 1010 and 1030 leaf
    -- from the JE ledger. Fixes the 86M-type drift between current_balance
    -- and SUM(debit - credit) that accumulated from prior code paths
    -- updating current_balance without matching JE lines.
    --
    -- Same shape as migration 407 but scoped to these two accounts so
    -- we don't risk overwriting balance recomputes that other accounts
    -- depend on.
    -- =================================================================
    WITH balances AS (
        SELECT a.id AS account_id,
               COALESCE(SUM(jel.debit_amount - jel.credit_amount), 0) AS ledger_sum
        FROM accounts a
        LEFT JOIN journal_entry_lines jel ON jel.account_id = a.id
        LEFT JOIN journal_entries je ON je.id = jel.journal_entry_id AND je.deleted_at IS NULL
        WHERE a.code IN ('1010', '1030')
          AND a.deleted_at IS NULL
        GROUP BY a.id
    )
    UPDATE accounts a
       SET current_balance = b.ledger_sum,
           updated_at      = NOW()
      FROM balances b
     WHERE a.id = b.account_id
       AND a.current_balance IS DISTINCT FROM b.ledger_sum;
    GET DIAGNOSTICS recomputed_count = ROW_COUNT;

    RAISE NOTICE
        'Migration 415 done: posted=% skipped_existing=% skipped_zero=% skipped_no_1030=% created_1010=% recomputed_balances=%',
        posted_count, skipped_existing, skipped_zero, skipped_no_1030, created_1010, recomputed_count;
END $$;

ALTER TABLE accounts            ENABLE TRIGGER trg_check_cash_bank_balance;
ALTER TABLE journal_entry_lines ENABLE TRIGGER trg_enforce_journal_line_invariants;

-- POST-DEPLOY VERIFICATION (EVROPLIT)
-- After this migration runs, expect:
--
--   SELECT code, name, current_balance FROM accounts
--   WHERE tenant_id = 'ce0b21e8-dde8-46d4-8cb3-fe78a6613e22'
--     AND organization_id = '465fb035-c265-49c3-88e6-0c1762f4efdc'
--     AND code IN ('1010','1030') ORDER BY code;
--
--   1010 = 1,776,561,121.34   (= 1,620,220,418.94 opening + 156,340,702.40 purchases)
--   1030 = 0.00
--
-- The Tovar-moddiy zaxiralar group total stays the same (value just
-- moved between sibling leaves). 1030 is now clean.
--
-- Residual discrepancy 1010 vs Ombor physical (1,595M) is ~181M and
-- corresponds to production consumption that DR'd WIP (2010) but did
-- not credit 1010 — that's bug #2 in the analysis and needs a
-- separate migration (or to be left for organic burn-down as
-- production runs settle).
