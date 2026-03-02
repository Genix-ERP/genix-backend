-- Migration 146: Ensure every organization has Bank and Cash journals
-- Previous migrations only created journals when j_count = 0,
-- but orgs that already had some journals (e.g. manually created) were skipped.
-- This migration ensures Bank and Cash journals exist for every org.

DO $$
DECLARE
    r RECORD;
    has_bank BOOLEAN;
    has_cash BOOLEAN;
BEGIN
    FOR r IN
        SELECT o.id as org_id, o.tenant_id
        FROM organizations o
        WHERE o.deleted_at IS NULL
    LOOP
        -- Check if bank journal exists
        SELECT EXISTS(
            SELECT 1 FROM journals
            WHERE tenant_id = r.tenant_id
              AND type = 'bank'
              AND deleted_at IS NULL
              AND (organization_id = r.org_id OR organization_id IS NULL)
        ) INTO has_bank;

        -- Check if cash journal exists
        SELECT EXISTS(
            SELECT 1 FROM journals
            WHERE tenant_id = r.tenant_id
              AND type = 'cash'
              AND deleted_at IS NULL
              AND (organization_id = r.org_id OR organization_id IS NULL)
        ) INTO has_cash;

        -- Create Bank journal if missing
        IF NOT has_bank THEN
            INSERT INTO journals (id, tenant_id, organization_id, code, name, type, is_active, created_at, updated_at)
            VALUES (gen_random_uuid(), r.tenant_id, r.org_id, 'BANK', 'Bank Journal', 'bank', true, NOW(), NOW())
            ON CONFLICT (tenant_id, code) DO UPDATE
                SET organization_id = COALESCE(journals.organization_id, EXCLUDED.organization_id),
                    deleted_at = NULL,
                    is_active = true,
                    type = 'bank',
                    updated_at = NOW();
        END IF;

        -- Create Cash journal if missing
        IF NOT has_cash THEN
            INSERT INTO journals (id, tenant_id, organization_id, code, name, type, is_active, created_at, updated_at)
            VALUES (gen_random_uuid(), r.tenant_id, r.org_id, 'CASH', 'Cash Journal', 'cash', true, NOW(), NOW())
            ON CONFLICT (tenant_id, code) DO UPDATE
                SET organization_id = COALESCE(journals.organization_id, EXCLUDED.organization_id),
                    deleted_at = NULL,
                    is_active = true,
                    type = 'cash',
                    updated_at = NOW();
        END IF;

        -- Also ensure General, Sales, Purchase, Misc exist
        IF NOT EXISTS(SELECT 1 FROM journals WHERE tenant_id = r.tenant_id AND type = 'general' AND deleted_at IS NULL AND (organization_id = r.org_id OR organization_id IS NULL)) THEN
            INSERT INTO journals (id, tenant_id, organization_id, code, name, type, is_active, created_at, updated_at)
            VALUES (gen_random_uuid(), r.tenant_id, r.org_id, 'GEN', 'General Journal', 'general', true, NOW(), NOW())
            ON CONFLICT (tenant_id, code) DO UPDATE SET organization_id = COALESCE(journals.organization_id, EXCLUDED.organization_id), deleted_at = NULL, is_active = true, updated_at = NOW();
        END IF;

        IF NOT EXISTS(SELECT 1 FROM journals WHERE tenant_id = r.tenant_id AND type = 'sales' AND deleted_at IS NULL AND (organization_id = r.org_id OR organization_id IS NULL)) THEN
            INSERT INTO journals (id, tenant_id, organization_id, code, name, type, is_active, created_at, updated_at)
            VALUES (gen_random_uuid(), r.tenant_id, r.org_id, 'SAL', 'Sales Journal', 'sales', true, NOW(), NOW())
            ON CONFLICT (tenant_id, code) DO UPDATE SET organization_id = COALESCE(journals.organization_id, EXCLUDED.organization_id), deleted_at = NULL, is_active = true, updated_at = NOW();
        END IF;

        IF NOT EXISTS(SELECT 1 FROM journals WHERE tenant_id = r.tenant_id AND type = 'purchase' AND deleted_at IS NULL AND (organization_id = r.org_id OR organization_id IS NULL)) THEN
            INSERT INTO journals (id, tenant_id, organization_id, code, name, type, is_active, created_at, updated_at)
            VALUES (gen_random_uuid(), r.tenant_id, r.org_id, 'PUR', 'Purchase Journal', 'purchase', true, NOW(), NOW())
            ON CONFLICT (tenant_id, code) DO UPDATE SET organization_id = COALESCE(journals.organization_id, EXCLUDED.organization_id), deleted_at = NULL, is_active = true, updated_at = NOW();
        END IF;

        IF NOT EXISTS(SELECT 1 FROM journals WHERE tenant_id = r.tenant_id AND type = 'miscellaneous' AND deleted_at IS NULL AND (organization_id = r.org_id OR organization_id IS NULL)) THEN
            INSERT INTO journals (id, tenant_id, organization_id, code, name, type, is_active, created_at, updated_at)
            VALUES (gen_random_uuid(), r.tenant_id, r.org_id, 'MISC', 'Miscellaneous Journal', 'miscellaneous', true, NOW(), NOW())
            ON CONFLICT (tenant_id, code) DO UPDATE SET organization_id = COALESCE(journals.organization_id, EXCLUDED.organization_id), deleted_at = NULL, is_active = true, updated_at = NOW();
        END IF;
    END LOOP;
END $$;
