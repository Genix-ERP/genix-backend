-- Migration 326: Flip journal invariants from soft (WARNING) to strict (EXCEPTION)
-- Reference: TT Buxgalteriya ERP §4.2 + §4.5
--
-- Migration 319 installed a permissive trigger that:
--   * RAISE WARNING on non-leaf accounts (TT §4.2)
--   * skipped mandatory_analytics entirely (TT §4.5)
-- so existing auto-posting paths wouldn't break during rollout.
--
-- Migration 325 installed an auto-enrichment trigger that fills analytics from
-- source documents, closing the §4.5 gap for all auto-posting callers.
--
-- We can now harden the enforcement trigger without breaking existing handlers.

CREATE OR REPLACE FUNCTION fn_enforce_journal_line_invariants()
RETURNS TRIGGER AS $$
DECLARE
    v_account_is_leaf      BOOLEAN;
    v_account_mandatory    BOOLEAN;
    v_account_code         TEXT;
    v_account_name         TEXT;
    v_analytics_types      JSONB;
    v_entry_status         TEXT;
    v_entry_date           DATE;
    v_entry_tenant         UUID;
    v_entry_source_type    TEXT;
    v_period_status        TEXT;
    v_period_locked        BOOLEAN;
    v_required_dim         TEXT;
BEGIN
    -- ---- DELETE: prevent deleting lines of a posted entry --------------
    IF TG_OP = 'DELETE' THEN
        SELECT status INTO v_entry_status
        FROM journal_entries
        WHERE id = OLD.journal_entry_id;

        IF v_entry_status = 'posted' THEN
            RAISE EXCEPTION
                'TT 4.4: posted journal entry is immutable, lines cannot be deleted (use storno instead)'
                USING ERRCODE = '23514';
        END IF;
        RETURN OLD;
    END IF;

    -- ---- UPDATE/INSERT: load parent entry context ----------------------
    SELECT status, entry_date, tenant_id, source_type
      INTO v_entry_status, v_entry_date, v_entry_tenant, v_entry_source_type
    FROM journal_entries
    WHERE id = NEW.journal_entry_id;

    IF TG_OP = 'UPDATE' AND v_entry_status = 'posted' THEN
        RAISE EXCEPTION
            'TT 4.4: posted journal entry is immutable (use storno instead)'
            USING ERRCODE = '23514';
    END IF;

    -- ---- Account metadata ---------------------------------------------
    SELECT COALESCE(is_leaf, true), COALESCE(mandatory_analytics, false),
           COALESCE(analytics_types, '[]'::jsonb), code, name
      INTO v_account_is_leaf, v_account_mandatory, v_analytics_types,
           v_account_code, v_account_name
    FROM accounts
    WHERE id = NEW.account_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'Account % not found', NEW.account_id
            USING ERRCODE = '23503';
    END IF;

    -- ---- is_leaf check (TT §4.2) — NOW HARD ----------------------------
    IF v_account_is_leaf = false THEN
        RAISE EXCEPTION
            'TT §4.2: account % (%) is a group account (is_leaf=false) — postings are only allowed on leaf accounts',
            v_account_code, v_account_name
            USING ERRCODE = '23514';
    END IF;

    -- ---- Period lock check (TT §4.3) -----------------------------------
    SELECT fp.status INTO v_period_status
    FROM fiscal_periods fp
    JOIN fiscal_years fy ON fp.fiscal_year_id = fy.id
    WHERE fy.tenant_id = v_entry_tenant
      AND v_entry_date BETWEEN fp.start_date AND fp.end_date
    LIMIT 1;

    IF v_period_status IN ('locked', 'closed') THEN
        RAISE EXCEPTION
            'TT §4.3: fiscal period for % is %; use storno in the current open period',
            v_entry_date, v_period_status
            USING ERRCODE = '23514';
    END IF;

    IF EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_schema = current_schema() AND table_name = 'accounting_periods'
    ) THEN
        SELECT COALESCE(is_locked, false) INTO v_period_locked
        FROM accounting_periods
        WHERE tenant_id = v_entry_tenant
          AND v_entry_date BETWEEN start_date AND end_date
          AND is_locked = true
        LIMIT 1;

        IF v_period_locked THEN
            RAISE EXCEPTION
                'TT §4.3: accounting period for % is locked; use storno in the current open period',
                v_entry_date
                USING ERRCODE = '23514';
        END IF;
    END IF;

    -- ---- Mandatory analytics check (TT §4.5) — NOW HARD ---------------
    -- Note: migration 325's enrichment trigger has already run (name prefix
    -- "a_") and populated analytics from source docs when possible. If a
    -- dimension is still NULL at this point, it means:
    --   - the entry is manual (no source doc), AND the caller didn't supply it
    --   - OR the source doc lacked the required field
    -- Both cases should be rejected so the buxgalter notices the gap.
    IF v_account_mandatory = true AND v_analytics_types IS NOT NULL THEN
        FOR v_required_dim IN
            SELECT jsonb_array_elements_text(v_analytics_types)
        LOOP
            IF v_required_dim = 'kontragent' AND NEW.contact_id IS NULL THEN
                RAISE EXCEPTION
                    'TT §4.5: account % (%) requires counterparty (kontragent); line source_type=%',
                    v_account_code, v_account_name, COALESCE(v_entry_source_type, 'manual')
                    USING ERRCODE = '23514';
            ELSIF v_required_dim = 'shartnoma' AND NEW.contract_id IS NULL THEN
                RAISE EXCEPTION
                    'TT §4.5: account % (%) requires contract (shartnoma); line source_type=%',
                    v_account_code, v_account_name, COALESCE(v_entry_source_type, 'manual')
                    USING ERRCODE = '23514';
            ELSIF v_required_dim = 'ombor' AND NEW.warehouse_id IS NULL THEN
                RAISE EXCEPTION
                    'TT §4.5: account % (%) requires warehouse (ombor); line source_type=%',
                    v_account_code, v_account_name, COALESCE(v_entry_source_type, 'manual')
                    USING ERRCODE = '23514';
            ELSIF v_required_dim = 'xodim' AND NEW.employee_id IS NULL THEN
                RAISE EXCEPTION
                    'TT §4.5: account % (%) requires employee (xodim); line source_type=%',
                    v_account_code, v_account_name, COALESCE(v_entry_source_type, 'manual')
                    USING ERRCODE = '23514';
            END IF;
        END LOOP;
    END IF;

    -- ---- amount_base default (TT §4.6) ---------------------------------
    IF NEW.amount_base IS NULL THEN
        IF NEW.exchange_rate IS NOT NULL AND NEW.exchange_rate > 0 THEN
            NEW.amount_base := (COALESCE(NEW.debit_amount, 0) + COALESCE(NEW.credit_amount, 0))
                               * NEW.exchange_rate;
        ELSE
            NEW.amount_base := COALESCE(NEW.debit_amount, 0) + COALESCE(NEW.credit_amount, 0);
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION fn_enforce_journal_line_invariants() IS
    'TT Buxgalteriya ERP §4 guard: is_leaf, period lock, posted immutability, mandatory analytics (NOW STRICT). All four rules raise EXCEPTION; migration 325 enrichment runs first to auto-fill analytics where possible.';
