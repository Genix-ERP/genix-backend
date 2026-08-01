-- 445_payroll_receivable_accounts.sql
--
-- Seeds the two 47xx personnel-receivable accounts that payroll code posts to
-- but no migration ever created (ish-haqi audit §2.5):
--
--   4720  Xodimlarga berilgan qarzlar
--         employee_loans.go createLoanJournalEntry: Dt 4720 / Kt cash at
--         disbursement; postLoanRepaymentJE: Dt cash / Kt 4720 at repayment.
--         Without the account both JEs silently no-op.
--
--   4730  Moddiy zararni qoplash bo'yicha hisob-kitoblar
--         payroll.go ConfirmSalaryPayment: Dt 6710 / Kt 4730 for inventory
--         shortage deductions (source_type='salary_deduction'). The guard at
--         payroll.go requires the account, so the JE has never posted on a
--         stock chart.
--
-- Both are leaf accounts (is_leaf = true, TT §4.2) under the 4700 "Turli
-- debitorlar" group from migration 317, type OA, nature ACTIVE, with employee
-- analytics available (non-mandatory; trigger 325 auto-enriches employee_id
-- for source types employee_loan / salary_deduction).
--
-- New-organization seeding: the Go seed list in
-- internal/handler/organizations.go gets the same two rows — migrations only
-- cover orgs that exist at migration time.

DO $$
DECLARE
    org_rec RECORD;
    type_map JSONB := '{}';
    type_record RECORD;
    acc RECORD;
    parent_4700 UUID;
    v_now TIMESTAMP WITH TIME ZONE := NOW();
BEGIN
    FOR type_record IN SELECT id, code FROM account_types LOOP
        type_map := type_map || jsonb_build_object(type_record.code, type_record.id::text);
    END LOOP;

    FOR org_rec IN
        SELECT DISTINCT o.id AS org_id, o.tenant_id
        FROM organizations o
        WHERE o.deleted_at IS NULL
    LOOP
        SELECT id INTO parent_4700 FROM accounts
        WHERE tenant_id = org_rec.tenant_id
          AND organization_id = org_rec.org_id
          AND code = '4700' AND deleted_at IS NULL
        LIMIT 1;

        FOR acc IN
            SELECT * FROM (VALUES
                ('4720', 'Xodimlarga berilgan qarzlar', 'Loans to Employees', 'Займы, выданные работникам',
                 'Xodimlarga berilgan qarzlar (ssudalar) bo''yicha hisob-kitoblar'),
                ('4730', 'Moddiy zararni qoplash bo''yicha hisob-kitoblar', 'Compensation for Material Damage', 'Расчёты по возмещению материального ущерба',
                 'Kamomad va moddiy zarar bo''yicha xodimlardan undirishlar')
            ) AS t(code, name_uz, name_en, name_ru, description)
        LOOP
            INSERT INTO accounts (
                id, tenant_id, organization_id, parent_id, account_type_id,
                code, name, name_en, name_ru, description,
                is_bank_account, is_control_account, is_reconcilable,
                current_balance, opening_balance, is_active, is_leaf, account_nature,
                analytics_types, mandatory_analytics,
                created_at, updated_at
            ) VALUES (
                uuid_generate_v4(), org_rec.tenant_id, org_rec.org_id, parent_4700,
                (type_map->>'OA')::uuid,
                acc.code, acc.name_uz, acc.name_en, acc.name_ru, acc.description,
                false, false, false,
                0, 0, true, true, 'ACTIVE',
                '["xodim"]'::jsonb, false,
                v_now, v_now
            )
            ON CONFLICT (tenant_id, organization_id, code) DO NOTHING;
        END LOOP;
    END LOOP;

    -- Keep the tree invariant: a group with children is non-leaf.
    UPDATE accounts a SET is_leaf = false
    WHERE a.code = '4700'
      AND a.deleted_at IS NULL
      AND EXISTS (
          SELECT 1 FROM accounts c
          WHERE c.parent_id = a.id AND c.deleted_at IS NULL
      );
END $$;
