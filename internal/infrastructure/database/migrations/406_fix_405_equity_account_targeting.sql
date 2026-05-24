-- 406_fix_405_equity_account_targeting.sql
--
-- Migration 405 was supposed to post `DR Inventory leaf / CR Equity leaf`
-- to recognize physically-present inventory that the GL had lost track of.
-- The credit-side account lookup ordered `3010 → 3000 → 3110` thinking
-- those were the equity codes, but in the NAS chart of accounts shipped
-- with GenixERP, 3xxx is deferred-expense ASSETS, not equity. Equity
-- lives at 8xxx (8310 Oddiy aksiyalar, 8400 Zaxira kapitali, 8500
-- Qo'shimcha kapital, 8600 Maqsadli tushumlar, 8710 Taqsimlanmagan foyda
-- — oldingi yillar, 8720 E'lon qilingan dividendlar).
--
-- Net effect of the misposting: the inventory DR side was correct (1030
-- went from 0 → +1,679,985,000), but the CR side landed on
-- `3110 Kelgusi davr xarajatlari (uzoq muddatli)` — a long-term deferred
-- expense asset — pulling that account to -1,679,985,000. The books
-- balance arithmetically, but the equity didn't actually move and a
-- deferred-expense asset now sits in the red for no business reason.
--
-- WHAT THIS MIGRATION DOES
-- For every JE produced by migration 405 (reference 'INV-OPENING-%'),
-- post a single correcting JE that:
--
--   DR  3110  (or whichever account 405 wrongly credited)  X    → returns it to 0
--   CR  8710 Taqsimlanmagan foyda (oldingi yillar)         X    → equity recognised
--
-- Net of 405 + 406:
--   1030  +X   (inventory recognised, unchanged from 405) ✓
--   3110   0   (was -X from 405, brought back by 406) ✓
--   8710  -X   (equity recognised, where it should have been all along) ✓
--
-- ACCOUNT SELECTION
-- We pick the equity destination using account_types.category = 'equity'
-- joined with code preference: 8710 > 8500 > 8400 > 8310. 8710 is the
-- proper bucket for prior-period inventory recognition; the others are
-- fallbacks for tenants whose chart somehow lacks 8710.
--
-- IDEMPOTENCY
-- Each correcting JE is tagged with reference =
-- 'INV-OPENING-EQUITY-FIX-<original-entry-number>'. The detection query
-- skips originals that already have a matching fix posted, so re-runs
-- are no-ops.
--
-- TRIGGER BYPASS (same pattern as 404/405)
-- Disable trg_enforce_journal_line_invariants and trg_check_cash_bank_balance
-- for this transaction only. PostgreSQL's transactional DDL ensures both
-- come back to ENABLED at COMMIT (success or rollback). We do NOT alter
-- the trigger functions themselves.

ALTER TABLE journal_entry_lines DISABLE TRIGGER trg_enforce_journal_line_invariants;
ALTER TABLE accounts            DISABLE TRIGGER trg_check_cash_bank_balance;

DO $$
DECLARE
    opening RECORD;
    v_wrong_acct_id    UUID;
    v_wrong_amount     NUMERIC;
    v_correct_acct_id  UUID;
    v_correct_acct_code TEXT;
    v_journal_id       UUID;
    v_next_seq         INT;
    v_je_id            UUID;
    v_entry_number     TEXT;
    v_reference        TEXT;
    fixed_count        INT := 0;
    skipped_no_acct    INT := 0;
    skipped_existing   INT := 0;
BEGIN
    FOR opening IN
        SELECT je.id, je.tenant_id, je.organization_id, je.journal_id,
               je.entry_number, je.entry_date
        FROM journal_entries je
        WHERE je.reference LIKE 'INV-OPENING-%'
          AND je.source_type = 'opening_balance'
          AND je.deleted_at IS NULL
          -- Idempotency: skip originals that already have a fix
          AND NOT EXISTS (
              SELECT 1 FROM journal_entries je2
              WHERE je2.tenant_id = je.tenant_id
                AND je2.reference = 'INV-OPENING-EQUITY-FIX-' || je.entry_number
                AND je2.deleted_at IS NULL
          )
    LOOP
        -- Locate the wrongly-credited account from the original 405 entry.
        -- The DR line went to inventory (1030 or similar), which is correct
        -- and stays. The CR line is what we need to undo.
        SELECT jel.account_id, jel.credit_amount
        INTO v_wrong_acct_id, v_wrong_amount
        FROM journal_entry_lines jel
        WHERE jel.journal_entry_id = opening.id
          AND jel.credit_amount > 0
        ORDER BY jel.line_number
        LIMIT 1;

        IF v_wrong_acct_id IS NULL OR v_wrong_amount IS NULL OR v_wrong_amount <= 0 THEN
            RAISE WARNING 'No CR line found in opening entry %; skipping', opening.entry_number;
            skipped_existing := skipped_existing + 1;
            CONTINUE;
        END IF;

        -- Pick the right equity leaf in this (tenant, org). Prefer 8710
        -- (Taqsimlanmagan foyda — oldingi yillar), then 8500 / 8400 / 8310.
        -- Structural filter (account_types.category = 'equity') is the
        -- safety net for tenants whose codes differ from the standard NAS.
        SELECT a.id, a.code
        INTO v_correct_acct_id, v_correct_acct_code
        FROM accounts a
        JOIN account_types at ON at.id = a.account_type_id
        WHERE a.tenant_id = opening.tenant_id
          AND a.organization_id = opening.organization_id
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

        IF v_correct_acct_id IS NULL THEN
            RAISE WARNING 'No equity leaf found in tenant=% org=%; skipping opening entry %',
                opening.tenant_id, opening.organization_id, opening.entry_number;
            skipped_no_acct := skipped_no_acct + 1;
            CONTINUE;
        END IF;

        -- Pick a journal — prefer MISC/GEN, fall back to STOCK
        SELECT j.id, COALESCE(j.next_number, 1)
        INTO v_journal_id, v_next_seq
        FROM journals j
        WHERE j.tenant_id = opening.tenant_id
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
            RAISE WARNING 'No journal for tenant=%; skipping', opening.tenant_id;
            skipped_no_acct := skipped_no_acct + 1;
            CONTINUE;
        END IF;

        v_reference := 'INV-OPENING-EQUITY-FIX-' || opening.entry_number;
        v_je_id := uuid_generate_v4();
        v_entry_number := 'OPENFIX' || LPAD(v_next_seq::text, 6, '0');

        INSERT INTO journal_entries (
            id, tenant_id, organization_id, journal_id, entry_number,
            entry_date, reference, description,
            source_type, source_id, exchange_rate,
            total_debit, total_credit, status,
            created_at, updated_at
        ) VALUES (
            v_je_id,
            opening.tenant_id,
            opening.organization_id,
            v_journal_id,
            v_entry_number,
            CURRENT_DATE,
            v_reference,
            'Re-route opening-balance equity credit from a non-equity asset ' ||
                '(originally posted by migration 405) to the proper equity ' ||
                'leaf (' || v_correct_acct_code || '). See migration 406.',
            'opening_balance_fix',
            opening.id,
            1.0,
            v_wrong_amount,
            v_wrong_amount,
            'posted',
            NOW(),
            NOW()
        );

        UPDATE journals SET next_number = next_number + 1, updated_at = NOW()
        WHERE id = v_journal_id;

        -- DR the wrong account → cancels the original CR, returns it to 0
        INSERT INTO journal_entry_lines (
            id, journal_entry_id, line_number, account_id,
            description, debit_amount, credit_amount,
            exchange_rate, amount_base, analytics_json, created_at
        ) VALUES (
            uuid_generate_v4(), v_je_id, 1, v_wrong_acct_id,
            'Reverse mistaken equity credit from migration 405',
            v_wrong_amount, 0, 1.0, v_wrong_amount, '{}'::jsonb, NOW()
        );

        UPDATE accounts
        SET current_balance = current_balance + v_wrong_amount, updated_at = NOW()
        WHERE id = v_wrong_acct_id;

        -- CR the correct equity leaf → recognises the inventory opening
        -- balance as prior-period retained earnings, where it belongs
        INSERT INTO journal_entry_lines (
            id, journal_entry_id, line_number, account_id,
            description, debit_amount, credit_amount,
            exchange_rate, amount_base, analytics_json, created_at
        ) VALUES (
            uuid_generate_v4(), v_je_id, 2, v_correct_acct_id,
            'Opening inventory recognized as equity (' || v_correct_acct_code || ')',
            0, v_wrong_amount, 1.0, v_wrong_amount, '{}'::jsonb, NOW()
        );

        UPDATE accounts
        SET current_balance = current_balance - v_wrong_amount, updated_at = NOW()
        WHERE id = v_correct_acct_id;

        fixed_count := fixed_count + 1;

        RAISE NOTICE 'Fixed opening % for tenant=% org=%: moved % from wrong account to equity leaf %',
            opening.entry_number, opening.tenant_id, opening.organization_id,
            v_wrong_amount, v_correct_acct_code;
    END LOOP;

    RAISE NOTICE 'Migration 406 done: fixed=% skipped_existing=% skipped_no_acct=%',
        fixed_count, skipped_existing, skipped_no_acct;
END $$;

ALTER TABLE accounts            ENABLE TRIGGER trg_check_cash_bank_balance;
ALTER TABLE journal_entry_lines ENABLE TRIGGER trg_enforce_journal_line_invariants;

-- POST-DEPLOY VERIFICATION
-- After running, the deferred-expenses account 3110 (or whichever
-- 3xxx account 405 wrongly hit) should return to 0, and the 8710
-- equity leaf should pick up the opening inventory amount.
--
--   SELECT a.code, a.name, a.is_leaf, a.current_balance, a.organization_id
--   FROM accounts a
--   WHERE a.code IN ('1030','3110','8710','8500')
--     AND a.deleted_at IS NULL
--   ORDER BY a.organization_id, a.code;
--
-- And see the new fix entries:
--
--   SELECT je.entry_number, je.reference, je.entry_date::date,
--          je.total_debit, je.total_credit, je.organization_id
--   FROM journal_entries je
--   WHERE je.source_type = 'opening_balance_fix'
--     AND je.deleted_at IS NULL
--   ORDER BY je.entry_date DESC, je.organization_id;
