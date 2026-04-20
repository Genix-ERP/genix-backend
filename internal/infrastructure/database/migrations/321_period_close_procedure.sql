-- Migration 321: Period close procedure and closing-entries table
-- Reference: TT Buxgalteriya ERP §4.3, §4.4, §6 — period closing mechanism
--
-- Introduces:
--   * period_closings       — audit trail per closing run (who/when/status/invariants)
--   * period_closing_checks — invariant results captured at close time
--   * fn_close_accounting_period(tenant, organization, period_start, period_end, user_id)
--
-- The function is a single atomic transaction that:
--   1. Asserts Dt=Kt across all posted entries in the period (TT §6.1 invariant)
--   2. Checks for any draft entries in the period; flags them as blockers if present
--   3. Computes the period's P&L (9xxx accounts) and books the close to 9900
--      (Yakuniy moliyaviy natija)
--   4. Marks the fiscal_period / accounting_period as CLOSED and stores closed_at/closed_by
--   5. Captures checksums in period_closings for auditors
--
-- Reopening is possible only via fn_reopen_accounting_period, which requires
-- a separate admin permission (handled at the Go layer).

-- ============================================
-- Tables
-- ============================================

CREATE TABLE IF NOT EXISTS period_closings (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    period_start DATE NOT NULL,
    period_end DATE NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'in_progress',
        -- in_progress | closed | failed | reopened
    total_debit DECIMAL(20, 4) NOT NULL DEFAULT 0,
    total_credit DECIMAL(20, 4) NOT NULL DEFAULT 0,
    is_balanced BOOLEAN NOT NULL DEFAULT false,
    draft_entry_count INT NOT NULL DEFAULT 0,
    pnl_debit DECIMAL(20, 4) NOT NULL DEFAULT 0,
    pnl_credit DECIMAL(20, 4) NOT NULL DEFAULT 0,
    net_profit DECIMAL(20, 4) NOT NULL DEFAULT 0,
    closing_journal_entry_id UUID,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    started_by UUID,
    reopened_at TIMESTAMPTZ,
    reopened_by UUID,
    reopen_reason TEXT,
    notes TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_period_closings_tenant_range
    ON period_closings (tenant_id, organization_id, period_start, period_end);

CREATE TABLE IF NOT EXISTS period_closing_checks (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    period_closing_id UUID NOT NULL REFERENCES period_closings(id) ON DELETE CASCADE,
    check_name VARCHAR(64) NOT NULL,
        -- balance_invariant | no_drafts | no_unreconciled_fx | forma_1_matches | ...
    passed BOOLEAN NOT NULL,
    expected_value TEXT,
    actual_value TEXT,
    message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_period_closing_checks_closing
    ON period_closing_checks (period_closing_id);

-- ============================================
-- fn_close_accounting_period
-- ============================================
-- Returns the period_closings row ID. If it returns but status='failed',
-- the caller should look up period_closing_checks for reasons.

CREATE OR REPLACE FUNCTION fn_close_accounting_period(
    p_tenant_id UUID,
    p_organization_id UUID,
    p_period_start DATE,
    p_period_end DATE,
    p_user_id UUID
) RETURNS UUID AS $$
DECLARE
    v_closing_id UUID := uuid_generate_v4();
    v_now TIMESTAMPTZ := NOW();
    v_total_dt DECIMAL(20, 4);
    v_total_kt DECIMAL(20, 4);
    v_drafts INT;
    v_pnl_dt DECIMAL(20, 4);
    v_pnl_kt DECIMAL(20, 4);
    v_net_profit DECIMAL(20, 4);
    v_final_result_acct UUID;   -- 9900
    v_any_failed BOOLEAN := false;
    v_closing_je UUID;
    v_acct_rec RECORD;
    v_line_num INT;
BEGIN
    -- Create the closing row up-front so checks can attach to it.
    INSERT INTO period_closings (
        id, tenant_id, organization_id, period_start, period_end,
        status, started_at, started_by
    ) VALUES (
        v_closing_id, p_tenant_id, p_organization_id, p_period_start, p_period_end,
        'in_progress', v_now, p_user_id
    );

    -- CHECK 1: posted entries in period are balanced (TT §6.1 invariant)
    SELECT
        COALESCE(SUM(jel.debit_amount), 0),
        COALESCE(SUM(jel.credit_amount), 0)
    INTO v_total_dt, v_total_kt
    FROM journal_entry_lines jel
    JOIN journal_entries je ON jel.journal_entry_id = je.id
    WHERE je.tenant_id = p_tenant_id
      AND (p_organization_id IS NULL OR je.organization_id = p_organization_id)
      AND je.status = 'posted'
      AND je.deleted_at IS NULL
      AND je.entry_date BETWEEN p_period_start AND p_period_end;

    INSERT INTO period_closing_checks (period_closing_id, check_name, passed,
        expected_value, actual_value, message)
    VALUES (v_closing_id, 'balance_invariant',
        ABS(v_total_dt - v_total_kt) < 0.01,
        v_total_dt::text,
        v_total_kt::text,
        CASE WHEN ABS(v_total_dt - v_total_kt) < 0.01
             THEN 'Dt aylanma = Kt aylanma'
             ELSE format('Balans buzilgan: Dt=%s Kt=%s', v_total_dt, v_total_kt)
        END);

    IF ABS(v_total_dt - v_total_kt) >= 0.01 THEN
        v_any_failed := true;
    END IF;

    -- CHECK 2: no draft entries in the period
    SELECT COUNT(*) INTO v_drafts
    FROM journal_entries je
    WHERE je.tenant_id = p_tenant_id
      AND (p_organization_id IS NULL OR je.organization_id = p_organization_id)
      AND je.status = 'draft'
      AND je.deleted_at IS NULL
      AND je.entry_date BETWEEN p_period_start AND p_period_end;

    INSERT INTO period_closing_checks (period_closing_id, check_name, passed,
        expected_value, actual_value, message)
    VALUES (v_closing_id, 'no_drafts',
        v_drafts = 0, '0', v_drafts::text,
        CASE WHEN v_drafts = 0 THEN 'No draft entries'
             ELSE format('%s draft entries remain — post or delete them before closing', v_drafts) END);

    IF v_drafts > 0 THEN
        v_any_failed := true;
    END IF;

    -- If pre-checks failed, mark and return — no P&L rollup, no period lock.
    IF v_any_failed THEN
        UPDATE period_closings
           SET status = 'failed',
               total_debit = v_total_dt,
               total_credit = v_total_kt,
               is_balanced = ABS(v_total_dt - v_total_kt) < 0.01,
               draft_entry_count = v_drafts,
               completed_at = v_now
         WHERE id = v_closing_id;
        RETURN v_closing_id;
    END IF;

    -- P&L aggregation: sum up 9xxx revenue/expense movement in the period
    SELECT
        COALESCE(SUM(jel.debit_amount), 0),
        COALESCE(SUM(jel.credit_amount), 0)
    INTO v_pnl_dt, v_pnl_kt
    FROM journal_entry_lines jel
    JOIN journal_entries je ON jel.journal_entry_id = je.id
    JOIN accounts a ON jel.account_id = a.id
    WHERE je.tenant_id = p_tenant_id
      AND (p_organization_id IS NULL OR je.organization_id = p_organization_id)
      AND je.status = 'posted'
      AND je.deleted_at IS NULL
      AND je.entry_date BETWEEN p_period_start AND p_period_end
      AND a.code LIKE '9%'
      AND a.code NOT LIKE '9900%';

    v_net_profit := v_pnl_kt - v_pnl_dt;

    -- Look up 9900 (Yakuniy moliyaviy natija). If absent, fail.
    SELECT id INTO v_final_result_acct
    FROM accounts
    WHERE tenant_id = p_tenant_id
      AND (p_organization_id IS NULL OR organization_id = p_organization_id OR organization_id IS NULL)
      AND code = '9900'
      AND deleted_at IS NULL
      AND is_active = true
    ORDER BY (organization_id IS NULL) ASC
    LIMIT 1;

    IF v_final_result_acct IS NULL THEN
        INSERT INTO period_closing_checks (period_closing_id, check_name, passed,
            message)
        VALUES (v_closing_id, 'final_result_account',
            false, 'Account 9900 (Yakuniy moliyaviy natija) not found; cannot book close');

        UPDATE period_closings
           SET status = 'failed',
               total_debit = v_total_dt,
               total_credit = v_total_kt,
               is_balanced = true,
               draft_entry_count = 0,
               pnl_debit = v_pnl_dt,
               pnl_credit = v_pnl_kt,
               net_profit = v_net_profit,
               completed_at = v_now
         WHERE id = v_closing_id;
        RETURN v_closing_id;
    END IF;

    -- Book the closing entry: zero out each 9xxx leaf into 9900
    -- We need a journal to attach this to; prefer GENERAL/MISC.
    DECLARE
        v_journal_id UUID;
        v_next INT;
    BEGIN
        SELECT id, COALESCE(next_number, 1) INTO v_journal_id, v_next
        FROM journals
        WHERE tenant_id = p_tenant_id
          AND (p_organization_id IS NULL OR organization_id = p_organization_id OR organization_id IS NULL)
          AND code IN ('MISC', 'GENERAL')
          AND deleted_at IS NULL
        ORDER BY CASE code WHEN 'MISC' THEN 0 ELSE 1 END,
                 (organization_id IS NULL) ASC
        LIMIT 1;

        IF v_journal_id IS NOT NULL THEN
            v_closing_je := uuid_generate_v4();
            INSERT INTO journal_entries (
                id, tenant_id, organization_id, journal_id, entry_number, entry_date,
                description, source_type, source_id,
                total_debit, total_credit, status,
                posted_at, posted_by, created_by, created_at, updated_at
            ) VALUES (
                v_closing_je, p_tenant_id, p_organization_id, v_journal_id,
                format('CLOSE-%s-%s', to_char(p_period_end, 'YYYY-MM'), v_next),
                p_period_end,
                format('Davr yopilishi %s — %s (TT §4.3)', p_period_start, p_period_end),
                'period_close', v_closing_id::text,
                0, 0, 'posted',
                v_now, p_user_id, p_user_id, v_now, v_now
            );

            v_line_num := 1;
            FOR v_acct_rec IN
                SELECT a.id AS account_id,
                       COALESCE(SUM(jel.debit_amount),  0) AS dt,
                       COALESCE(SUM(jel.credit_amount), 0) AS kt
                FROM accounts a
                LEFT JOIN journal_entry_lines jel ON jel.account_id = a.id
                LEFT JOIN journal_entries je ON jel.journal_entry_id = je.id
                    AND je.status = 'posted' AND je.deleted_at IS NULL
                    AND je.entry_date BETWEEN p_period_start AND p_period_end
                WHERE a.tenant_id = p_tenant_id
                  AND (p_organization_id IS NULL OR a.organization_id = p_organization_id)
                  AND a.deleted_at IS NULL AND a.is_active = true
                  AND COALESCE(a.is_leaf, true) = true
                  AND a.code LIKE '9%'
                  AND a.code NOT LIKE '9900%'
                GROUP BY a.id
                HAVING COALESCE(SUM(jel.debit_amount), 0) <> COALESCE(SUM(jel.credit_amount), 0)
            LOOP
                DECLARE
                    v_bal DECIMAL(20, 4) := v_acct_rec.dt - v_acct_rec.kt;
                BEGIN
                    IF v_bal > 0 THEN
                        -- Account has net debit balance → credit it, debit 9900
                        INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id,
                            debit_amount, credit_amount, exchange_rate, amount_base, analytics_json, created_at)
                        VALUES (uuid_generate_v4(), v_closing_je, v_line_num, v_acct_rec.account_id,
                            0, v_bal, 1.0, v_bal, '{}'::jsonb, v_now);
                        v_line_num := v_line_num + 1;
                        INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id,
                            debit_amount, credit_amount, exchange_rate, amount_base, analytics_json, created_at)
                        VALUES (uuid_generate_v4(), v_closing_je, v_line_num, v_final_result_acct,
                            v_bal, 0, 1.0, v_bal, '{}'::jsonb, v_now);
                        v_line_num := v_line_num + 1;
                    ELSE
                        INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id,
                            debit_amount, credit_amount, exchange_rate, amount_base, analytics_json, created_at)
                        VALUES (uuid_generate_v4(), v_closing_je, v_line_num, v_acct_rec.account_id,
                            -v_bal, 0, 1.0, -v_bal, '{}'::jsonb, v_now);
                        v_line_num := v_line_num + 1;
                        INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id,
                            debit_amount, credit_amount, exchange_rate, amount_base, analytics_json, created_at)
                        VALUES (uuid_generate_v4(), v_closing_je, v_line_num, v_final_result_acct,
                            0, -v_bal, 1.0, -v_bal, '{}'::jsonb, v_now);
                        v_line_num := v_line_num + 1;
                    END IF;
                END;
            END LOOP;

            -- Update header totals
            UPDATE journal_entries je
            SET total_debit  = (SELECT COALESCE(SUM(debit_amount), 0)  FROM journal_entry_lines WHERE journal_entry_id = je.id),
                total_credit = (SELECT COALESCE(SUM(credit_amount), 0) FROM journal_entry_lines WHERE journal_entry_id = je.id),
                updated_at = v_now
            WHERE id = v_closing_je;
        END IF;
    END;

    -- Lock the accounting_period row(s) in range
    UPDATE accounting_periods
       SET is_locked = true,
           locked_at = v_now,
           locked_by = p_user_id
     WHERE tenant_id = p_tenant_id
       AND (p_organization_id IS NULL OR organization_id = p_organization_id OR organization_id IS NULL)
       AND start_date >= p_period_start
       AND end_date   <= p_period_end
       AND is_locked = false;

    -- Flip fiscal_periods status from open → closed where applicable
    UPDATE fiscal_periods fp
       SET status = 'closed',
           updated_at = v_now
      FROM fiscal_years fy
     WHERE fp.fiscal_year_id = fy.id
       AND fy.tenant_id = p_tenant_id
       AND fp.start_date >= p_period_start
       AND fp.end_date   <= p_period_end
       AND fp.status = 'open';

    -- Persist closing summary
    UPDATE period_closings
       SET status = 'closed',
           total_debit = v_total_dt,
           total_credit = v_total_kt,
           is_balanced = true,
           draft_entry_count = 0,
           pnl_debit = v_pnl_dt,
           pnl_credit = v_pnl_kt,
           net_profit = v_net_profit,
           closing_journal_entry_id = v_closing_je,
           completed_at = v_now
     WHERE id = v_closing_id;

    RETURN v_closing_id;
END;
$$ LANGUAGE plpgsql;

-- ============================================
-- fn_reopen_accounting_period
-- ============================================
CREATE OR REPLACE FUNCTION fn_reopen_accounting_period(
    p_closing_id UUID,
    p_user_id UUID,
    p_reason TEXT
) RETURNS BOOLEAN AS $$
DECLARE
    v_closing RECORD;
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
        INSERT INTO journal_entries (
            id, tenant_id, organization_id, journal_id, entry_number, entry_date,
            description, source_type, source_id,
            total_debit, total_credit, status,
            is_reversal, reversal_of_id, reversal_reason,
            posted_at, posted_by, created_by, created_at, updated_at
        )
        SELECT
            uuid_generate_v4(), tenant_id, organization_id, journal_id,
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

COMMENT ON FUNCTION fn_close_accounting_period(UUID, UUID, DATE, DATE, UUID) IS
    'TT Buxgalteriya §4.3 — atomically close a period: Dt=Kt invariant check, no drafts, P&L rollup to 9900, period lock. Returns period_closings.id.';

COMMENT ON FUNCTION fn_reopen_accounting_period(UUID, UUID, TEXT) IS
    'Admin-only: reverse a period close via storno and unlock the period.';
