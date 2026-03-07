-- Chart of Accounts fixes per TZ: Genix_TZ_HisobRejasi
-- 1. Add CONTRA_ASSET account type
-- 2. Fix account types for 1510 (Accumulated Depreciation) and 1210 (Allowance for Doubtful Accounts)
-- 3. Soft-delete duplicate account 4200 (Product Sales, duplicates 4000 Sales Revenue)
-- 4. Add new accounts: 1020, 1340, 1410, 1700, 1730, 4300
-- 5. Reassign any journal_entry_lines from 4200 to 4000 before soft-deleting

-- Step 1: Add CONTRA_ASSET account type
INSERT INTO account_types (code, name, category, normal_balance, is_system)
VALUES ('CONTRA_ASSET', 'Contra-Asset', 'asset', 'credit', true)
ON CONFLICT (code) DO NOTHING;

-- Step 2: Update 1510 and 1210 to use CONTRA_ASSET type
DO $$
DECLARE
    contra_type_id UUID;
BEGIN
    SELECT id INTO contra_type_id FROM account_types WHERE code = 'CONTRA_ASSET';
    IF contra_type_id IS NULL THEN
        RAISE EXCEPTION 'CONTRA_ASSET account type not found';
    END IF;

    -- Update Accumulated Depreciation (1510) to contra-asset
    UPDATE accounts
    SET account_type_id = contra_type_id, updated_at = NOW()
    WHERE code = '1510' AND deleted_at IS NULL;

    -- Update Allowance for Doubtful Accounts (1210) to contra-asset
    UPDATE accounts
    SET account_type_id = contra_type_id, updated_at = NOW()
    WHERE code = '1210' AND deleted_at IS NULL;
END $$;

-- Step 3: Soft-delete 4200 (Product Sales) — reassign journal lines first
DO $$
DECLARE
    org_rec RECORD;
    acct_4200_id UUID;
    acct_4000_id UUID;
BEGIN
    -- For each organization, reassign 4200 lines to 4000 then soft-delete 4200
    FOR org_rec IN
        SELECT DISTINCT tenant_id, organization_id
        FROM accounts
        WHERE code = '4200' AND deleted_at IS NULL
    LOOP
        SELECT id INTO acct_4200_id
        FROM accounts
        WHERE tenant_id = org_rec.tenant_id
          AND organization_id = org_rec.organization_id
          AND code = '4200' AND deleted_at IS NULL;

        SELECT id INTO acct_4000_id
        FROM accounts
        WHERE tenant_id = org_rec.tenant_id
          AND organization_id = org_rec.organization_id
          AND code = '4000' AND deleted_at IS NULL;

        IF acct_4200_id IS NOT NULL AND acct_4000_id IS NOT NULL THEN
            -- Reassign any journal entry lines from 4200 to 4000
            UPDATE journal_entry_lines
            SET account_id = acct_4000_id
            WHERE account_id = acct_4200_id;

            -- Transfer balance to 4000
            UPDATE accounts
            SET current_balance = current_balance + (
                SELECT COALESCE(current_balance, 0) FROM accounts WHERE id = acct_4200_id
            ), updated_at = NOW()
            WHERE id = acct_4000_id;
        END IF;

        -- Soft-delete 4200
        IF acct_4200_id IS NOT NULL THEN
            UPDATE accounts
            SET deleted_at = NOW(), is_active = false, current_balance = 0, updated_at = NOW()
            WHERE id = acct_4200_id;
        END IF;
    END LOOP;
END $$;

-- Step 4: Add new accounts for all existing organizations
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
                ('1020', 'Foreign Currency Account', 'CASH', false, false, true, 'Foreign currency bank account'),
                ('1340', 'Goods for Resale', 'INV', false, false, false, 'Goods purchased for resale'),
                ('1410', 'Input VAT', 'OA', false, false, false, 'VAT on purchases (receivable)'),
                ('1700', 'Construction Costs', 'OA', false, false, false, 'Construction work in progress costs'),
                ('1730', 'Employee Advances', 'OA', false, false, false, 'Advances paid to employees'),
                ('4300', 'Construction Revenue', 'REVENUE', false, false, false, 'Revenue from construction projects')
            ) AS t(code, name, type_code, is_bank, is_control, is_recon, description)
        LOOP
            INSERT INTO accounts (
                id, tenant_id, organization_id, account_type_id, code, name, description,
                is_bank_account, is_control_account, is_reconcilable,
                current_balance, opening_balance, is_active, created_at, updated_at
            ) VALUES (
                uuid_generate_v4(), org_record.tenant_id, org_record.org_id,
                (type_map->>acc.type_code)::uuid,
                acc.code, acc.name, acc.description,
                acc.is_bank, acc.is_control, acc.is_recon,
                0, 0, true, v_now, v_now
            )
            ON CONFLICT (tenant_id, organization_id, code) DO NOTHING;
        END LOOP;
    END LOOP;
END $$;
