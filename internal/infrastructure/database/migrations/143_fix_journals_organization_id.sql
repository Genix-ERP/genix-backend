-- Migration 143: Fix journals missing organization_id
-- Journals created by createDefaultJournals were inserted without organization_id (NULL).
-- ListJournals filters by organization_id, so NULL journals were invisible.
-- This migration assigns the correct organization_id to journals that have tenant_id but no organization_id.

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

-- Seed default journals for any tenant/org that has no journals at all
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
          AND organization_id = r.org_id
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
                SET organization_id = EXCLUDED.organization_id,
                    updated_at = NOW()
            WHERE journals.organization_id IS NULL;
        END IF;
    END LOOP;
END $$;
