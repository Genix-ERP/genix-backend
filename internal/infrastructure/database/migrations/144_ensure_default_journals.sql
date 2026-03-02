-- Migration 144: Ensure default journals exist for all organizations
-- Previous migration 143 may have run but failed to create journals due to:
-- 1. Soft-deleted journals blocking UNIQUE(tenant_id, code) constraint
-- 2. organization_id still NULL after update

-- Step 1: Restore any soft-deleted default journals
UPDATE journals
SET deleted_at = NULL, is_active = true, updated_at = NOW()
WHERE deleted_at IS NOT NULL
  AND code IN ('GEN', 'SAL', 'PUR', 'CASH', 'BANK', 'MISC', 'GENERAL', 'SALES', 'PURCHASE', 'CASH_RECEIPTS');

-- Step 2: Set organization_id on journals that still have NULL
DO $$
DECLARE
    r RECORD;
BEGIN
    FOR r IN
        SELECT DISTINCT j.tenant_id, o.id as org_id
        FROM journals j
        JOIN organizations o ON o.tenant_id = j.tenant_id AND o.deleted_at IS NULL
        WHERE j.organization_id IS NULL AND j.deleted_at IS NULL
    LOOP
        UPDATE journals
        SET organization_id = r.org_id, updated_at = NOW()
        WHERE tenant_id = r.tenant_id
          AND organization_id IS NULL
          AND deleted_at IS NULL;
    END LOOP;
END $$;

-- Step 3: For orgs with 0 active journals, force-create them using upsert
DO $$
DECLARE
    r RECORD;
    j_count INT;
BEGIN
    FOR r IN
        SELECT o.id as org_id, o.tenant_id
        FROM organizations o
        WHERE o.deleted_at IS NULL
    LOOP
        SELECT COUNT(*) INTO j_count
        FROM journals
        WHERE tenant_id = r.tenant_id
          AND (organization_id = r.org_id OR organization_id IS NULL)
          AND deleted_at IS NULL;

        IF j_count = 0 THEN
            INSERT INTO journals (id, tenant_id, organization_id, code, name, type, is_active, created_at)
            VALUES
                (gen_random_uuid(), r.tenant_id, r.org_id, 'GEN',  'General Journal',      'general',       true, NOW()),
                (gen_random_uuid(), r.tenant_id, r.org_id, 'SAL',  'Sales Journal',         'sales',         true, NOW()),
                (gen_random_uuid(), r.tenant_id, r.org_id, 'PUR',  'Purchase Journal',      'purchase',      true, NOW()),
                (gen_random_uuid(), r.tenant_id, r.org_id, 'CASH', 'Cash Journal',          'cash',          true, NOW()),
                (gen_random_uuid(), r.tenant_id, r.org_id, 'BANK', 'Bank Journal',          'bank',          true, NOW()),
                (gen_random_uuid(), r.tenant_id, r.org_id, 'MISC', 'Miscellaneous Journal', 'miscellaneous', true, NOW())
            ON CONFLICT (tenant_id, code) DO UPDATE
                SET organization_id = COALESCE(journals.organization_id, EXCLUDED.organization_id),
                    deleted_at = NULL,
                    is_active = true,
                    updated_at = NOW();
        END IF;
    END LOOP;
END $$;
