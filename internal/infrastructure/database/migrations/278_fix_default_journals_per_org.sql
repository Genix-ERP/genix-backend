-- Fix: Ensure all 11 default journals exist for EVERY organization (company).
-- Previous migrations created journals at tenant level or only for the first org.
-- Root cause: the old UNIQUE(tenant_id, code) constraint prevented multiple orgs
-- from having the same journal code. We drop it and keep only (tenant_id, organization_id, code).

-- Step 1: Drop the old tenant-level unique constraint that blocks per-org journals
ALTER TABLE journals DROP CONSTRAINT IF EXISTS journals_tenant_id_code_key;

-- Step 2: Ensure the per-org unique constraint exists
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'journals_tenant_org_code_unique'
    ) THEN
        ALTER TABLE journals ADD CONSTRAINT journals_tenant_org_code_unique
            UNIQUE (tenant_id, organization_id, code);
    END IF;
END $$;

-- Step 3: Insert 11 default journals for every organization that is missing them
DO $$
DECLARE
    org RECORD;
    j RECORD;
BEGIN
    FOR org IN
        SELECT o.id AS org_id, o.tenant_id
        FROM organizations o
        WHERE o.deleted_at IS NULL
    LOOP
        -- Loop through the 11 default journals
        FOR j IN
            SELECT * FROM (VALUES
                ('BANK',          'Банковский журнал',                'Bank jurnali',                'Bank Journal',           'bank',          'BANK'),
                ('CASH',          'Кассовый журнал',                  'Kassa jurnali',               'Cash Journal',           'cash',          'CASH'),
                ('CASH_RECEIPTS', 'Журнал кассовых поступлений',      'Naqd pul tushumlari jurnali', 'Cash Receipts Journal',  'cash',          'CR'),
                ('GEN',           'Главный журнал',                   'Bosh jurnal',                 'General Journal',        'general',       'GEN'),
                ('MISC',          'Прочие операции',                  'Boshqa operatsiyalar jurnali','Miscellaneous Journal',  'general',       'MISC'),
                ('PUR',           'Журнал закупок',                   'Xarid jurnali',               'Purchase Journal',       'purchase',      'PUR'),
                ('SAL',           'Журнал продаж',                    'Sotish jurnali',              'Sales Journal',          'sale',          'SAL'),
                ('STOCK',         'Складской журнал',                 'Ombor jurnali',               'Stock Journal',          'general',       'STK'),
                ('ASSET',         'Журнал основных средств',          'Asosiy vositalar jurnali',    'Fixed Assets Journal',   'general',       'AST'),
                ('PAYROLL',       'Журнал зарплаты',                  'Ish haqi jurnali',            'Payroll Journal',        'general',       'PAY'),
                ('CONST',         'Строительный журнал',              'Qurilish jurnali',            'Construction Journal',   'general',       'CON')
            ) AS t(code, name_ru, name_uz, name_en, jtype, prefix)
        LOOP
            -- Only insert if this org doesn't already have this journal code
            IF NOT EXISTS (
                SELECT 1 FROM journals
                WHERE tenant_id = org.tenant_id
                  AND organization_id = org.org_id
                  AND code = j.code
                  AND deleted_at IS NULL
            ) THEN
                INSERT INTO journals (
                    id, tenant_id, organization_id, code, name, name_uz, name_en,
                    type, auto_sequence, next_number, number_prefix,
                    is_active, created_at, updated_at
                ) VALUES (
                    gen_random_uuid(), org.tenant_id, org.org_id, j.code, j.name_ru, j.name_uz, j.name_en,
                    j.jtype, true, 1, j.prefix,
                    true, NOW(), NOW()
                );
            END IF;
        END LOOP;
    END LOOP;
END $$;
