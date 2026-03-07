-- Fixes from Genix_TZ_MISC_Jurnal.docx review:
-- 1. Update CONTRA_ASSET category to 'contra_asset' (frontend needs separate category)
-- 2. Add missing accounts: 6710, 6720, 9110, 9430
-- 3. Fix payment method → journal mapping (Naqd → CASH, not BANK)

-- 1. Fix CONTRA_ASSET category so frontend can distinguish it
UPDATE account_types SET category = 'contra_asset' WHERE code = 'CONTRA_ASSET';

-- 2. Add missing accounts for all organizations
DO $$
DECLARE
    org_record RECORD;
    type_map JSONB := '{}';
    type_record RECORD;
    acc RECORD;
    v_now TIMESTAMP WITH TIME ZONE := NOW();
BEGIN
    -- Build account type map
    FOR type_record IN SELECT id, code FROM account_types LOOP
        type_map := type_map || jsonb_build_object(type_record.code, type_record.id::text);
    END LOOP;

    FOR org_record IN
        SELECT o.id AS org_id, o.tenant_id
        FROM organizations o
        WHERE o.deleted_at IS NULL
    LOOP
        FOR acc IN
            SELECT * FROM (VALUES
                ('6710', 'Ish haqi xarajati',          'Salary Expense',        'OPEX',      'Xodimlar ish haqi xarajatlari'),
                ('6720', 'To''lanmagan ish haqi',      'Accrued Salaries',      'ST_LIAB',   'Hali to''lanmagan ish haqi majburiyati'),
                ('9110', 'Boshqa daromadlar',           'Other Income',          'OTHER_INC', 'Kassa ortiqchasi, inventarizatsiya ortiqchasi'),
                ('9430', 'Boshqa zararlar',             'Other Losses',          'OTHER_EXP', 'Kamomad, hisobdan chiqarish, zarar')
            ) AS t(code, name_uz, name_en, type_code, description)
        LOOP
            INSERT INTO accounts (
                id, tenant_id, organization_id, account_type_id, code, name, name_uz, name_en, description,
                is_bank_account, is_control_account, is_reconcilable,
                current_balance, opening_balance, is_active, created_at, updated_at
            ) VALUES (
                gen_random_uuid(), org_record.tenant_id, org_record.org_id,
                (type_map->>acc.type_code)::uuid,
                acc.code, acc.name_en, acc.name_uz, acc.name_en, acc.description,
                false, false, false,
                0, 0, true, v_now, v_now
            )
            ON CONFLICT (tenant_id, organization_id, code) DO NOTHING;
        END LOOP;
    END LOOP;
END $$;

-- 3. Fix payment method → journal mapping
-- "Naqd" (CASH) should go to CASH journal, not BANK
-- First, remove any wrong CASH→BANK mapping
DELETE FROM journal_payment_methods
WHERE payment_method_id IN (SELECT id FROM payment_methods WHERE code = 'CASH')
  AND journal_id IN (SELECT id FROM journals WHERE type = 'bank');

-- Ensure CASH payment method is linked to CASH journal (for all tenants)
INSERT INTO journal_payment_methods (id, tenant_id, journal_id, payment_method_id, direction, created_at)
SELECT gen_random_uuid(), j.tenant_id, j.id, pm.id, d.direction, NOW()
FROM journals j
CROSS JOIN payment_methods pm
CROSS JOIN (VALUES ('inbound'), ('outbound')) AS d(direction)
WHERE j.type = 'cash' AND j.deleted_at IS NULL
  AND pm.code = 'CASH'
  AND NOT EXISTS (
    SELECT 1 FROM journal_payment_methods jpm
    WHERE jpm.journal_id = j.id AND jpm.payment_method_id = pm.id AND jpm.direction = d.direction
  );
