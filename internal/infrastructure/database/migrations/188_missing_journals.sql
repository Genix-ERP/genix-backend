-- Add missing journals: STOCK, ASSET, CONST, PURCH, PAYROLL
-- Cleanup: remove "A - asasas" and duplicate "JRN - терминал" (TERMINAL)
-- Check: merge CASH_1 into CASH if both exist

-- 1. Cleanup: soft-delete junk/duplicate journals (safe — avoids FK issues)
UPDATE journals SET deleted_at = NOW(), is_active = false
WHERE (code = 'A' AND name ILIKE '%asasas%')
   OR (code = 'JRN' AND name ILIKE '%терминал%')
   OR (code = 'JRN' AND name ILIKE '%terminal%');

-- 2. If CASH_1 exists alongside CASH, reassign its journal entries to CASH and delete it
DO $$
DECLARE
    r RECORD;
BEGIN
    FOR r IN
        SELECT c1.id AS cash1_id, c.id AS cash_id, c1.tenant_id
        FROM journals c1
        JOIN journals c ON c.tenant_id = c1.tenant_id AND c.code = 'CASH' AND c.deleted_at IS NULL
        WHERE c1.code = 'CASH_1' AND c1.deleted_at IS NULL
    LOOP
        UPDATE journal_entries SET journal_id = r.cash_id WHERE journal_id = r.cash1_id;
        UPDATE payments SET journal_id = r.cash_id WHERE journal_id = r.cash1_id;
        DELETE FROM journal_payment_methods WHERE journal_id = r.cash1_id;
        DELETE FROM recurring_journal_templates WHERE journal_id = r.cash1_id;
        DELETE FROM journals WHERE id = r.cash1_id;
    END LOOP;
END $$;

-- 3. Seed missing journals for all tenants
DO $$
DECLARE
    tnt RECORD;
    j RECORD;
    v_org_id UUID;
BEGIN
    FOR tnt IN SELECT id FROM tenants LOOP
        -- Get default organization for this tenant
        SELECT o.id INTO v_org_id
        FROM organizations o
        WHERE o.tenant_id = tnt.id AND o.deleted_at IS NULL
        ORDER BY o.created_at
        LIMIT 1;

        FOR j IN
            SELECT * FROM (VALUES
                ('STOCK',   'Ombor jurnali',              'stock'),
                ('ASSET',   'Asosiy vositalar jurnali',   'asset'),
                ('CONST',   'Qurilish jurnali',           'construction'),
                ('PURCH',   'Xarid jurnali',              'purchase'),
                ('PAYROLL', 'Ish haqi jurnali',            'payroll')
            ) AS t(code, name, type)
        LOOP
            INSERT INTO journals (id, tenant_id, organization_id, code, name, type, is_active, created_at, updated_at)
            VALUES (gen_random_uuid(), tnt.id, v_org_id, j.code, j.name, j.type, true, NOW(), NOW())
            ON CONFLICT (tenant_id, code) DO NOTHING;
        END LOOP;
    END LOOP;
END $$;
