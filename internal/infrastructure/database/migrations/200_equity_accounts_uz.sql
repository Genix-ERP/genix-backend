-- Add missing Uzbek-standard equity accounts and update names

-- Add missing equity accounts for all organizations that don't have them
DO $$
DECLARE
    org_record RECORD;
    type_id_equity UUID;
    type_id_retain UUID;
BEGIN
    SELECT id INTO type_id_equity FROM account_types WHERE code = 'EQUITY' LIMIT 1;
    SELECT id INTO type_id_retain FROM account_types WHERE code = 'RETAIN' LIMIT 1;

    FOR org_record IN
        SELECT o.id AS org_id, o.tenant_id
        FROM organizations o
        WHERE o.deleted_at IS NULL
    LOOP
        -- 3500 Taqsimlanmagan foyda (Retained Earnings proper)
        INSERT INTO accounts (id, tenant_id, organization_id, account_type_id, code, name, name_uz, name_en, is_active, created_at, updated_at)
        VALUES (uuid_generate_v4(), org_record.tenant_id, org_record.org_id, type_id_retain,
                '3500', 'Retained Earnings', 'Taqsimlanmagan foyda', 'Retained Earnings', true, NOW(), NOW())
        ON CONFLICT (tenant_id, organization_id, code) DO NOTHING;

        -- 3600 Joriy yil foydasi (Current Year Profit)
        INSERT INTO accounts (id, tenant_id, organization_id, account_type_id, code, name, name_uz, name_en, is_active, created_at, updated_at)
        VALUES (uuid_generate_v4(), org_record.tenant_id, org_record.org_id, type_id_retain,
                '3600', 'Current Year Profit', 'Joriy yil foydasi', 'Current Year Profit', true, NOW(), NOW())
        ON CONFLICT (tenant_id, organization_id, code) DO NOTHING;
    END LOOP;
END $$;

-- Update Uzbek names for existing equity accounts
UPDATE accounts SET name_uz = 'Eganing kapitali' WHERE code = '3000' AND (name_uz IS NULL OR name_uz = '');
UPDATE accounts SET name_uz = 'Ustav kapitali' WHERE code = '3100' AND (name_uz IS NULL OR name_uz = '');
UPDATE accounts SET name_uz = 'Taqsimlanmagan foyda' WHERE code = '3200' AND (name_uz IS NULL OR name_uz = '');
UPDATE accounts SET name_uz = 'Joriy yil foydasi' WHERE code = '3300' AND (name_uz IS NULL OR name_uz = '');
UPDATE accounts SET name_uz = 'Dividendlar' WHERE code = '3400' AND (name_uz IS NULL OR name_uz = '');
