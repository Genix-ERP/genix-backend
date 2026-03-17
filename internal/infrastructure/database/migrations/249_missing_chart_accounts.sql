-- Add missing chart of accounts: 0100, 0810, 2930
-- These are required by TZ (Genix_TZ_HisobRejasi)

DO $$
DECLARE
    org_record RECORD;
    type_map   jsonb := '{}';
    type_record RECORD;
BEGIN
    FOR type_record IN SELECT id, code FROM account_types LOOP
        type_map := type_map || jsonb_build_object(type_record.code, type_record.id::text);
    END LOOP;

    FOR org_record IN
        SELECT o.id AS org_id, o.tenant_id
        FROM organizations o
        WHERE o.deleted_at IS NULL
    LOOP
        -- 0100 - Fixed Assets (Asosiy vositalar)
        INSERT INTO accounts (
            id, tenant_id, organization_id, account_type_id, code, name, description,
            is_bank_account, is_control_account, is_reconcilable,
            current_balance, opening_balance, is_active, created_at, updated_at
        )
        SELECT
            uuid_generate_v4(), org_record.tenant_id, org_record.org_id,
            (type_map->>'FA')::uuid,
            '0100', 'Asosiy vositalar', 'Fixed Assets - buildings, equipment, vehicles',
            false, false, false, 0, 0, true, NOW(), NOW()
        WHERE NOT EXISTS (
            SELECT 1 FROM accounts
            WHERE tenant_id = org_record.tenant_id
              AND organization_id = org_record.org_id
              AND code = '0100'
              AND deleted_at IS NULL
        );

        -- 0810 - Capital Investments (Kapital qo'yilmalar)
        INSERT INTO accounts (
            id, tenant_id, organization_id, account_type_id, code, name, description,
            is_bank_account, is_control_account, is_reconcilable,
            current_balance, opening_balance, is_active, created_at, updated_at
        )
        SELECT
            uuid_generate_v4(), org_record.tenant_id, org_record.org_id,
            (type_map->>'OA')::uuid,
            '0810', 'Kapital qo''yilmalar', 'Capital Investments - assets under construction/installation',
            false, false, false, 0, 0, true, NOW(), NOW()
        WHERE NOT EXISTS (
            SELECT 1 FROM accounts
            WHERE tenant_id = org_record.tenant_id
              AND organization_id = org_record.org_id
              AND code = '0810'
              AND deleted_at IS NULL
        );

        -- 2930 - Deferred Expenses (Kelgusi davr xarajatlari)
        INSERT INTO accounts (
            id, tenant_id, organization_id, account_type_id, code, name, description,
            is_bank_account, is_control_account, is_reconcilable,
            current_balance, opening_balance, is_active, created_at, updated_at
        )
        SELECT
            uuid_generate_v4(), org_record.tenant_id, org_record.org_id,
            (type_map->>'OA')::uuid,
            '2930', 'Kelgusi davr xarajatlari', 'Deferred Expenses - prepaid expenses carried to future periods',
            false, false, false, 0, 0, true, NOW(), NOW()
        WHERE NOT EXISTS (
            SELECT 1 FROM accounts
            WHERE tenant_id = org_record.tenant_id
              AND organization_id = org_record.org_id
              AND code = '2930'
              AND deleted_at IS NULL
        );
    END LOOP;
END $$;
