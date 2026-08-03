-- 455_fa_categories_match_chart.sql
--
-- The 437 fa_categories seed used the textbook NSBU layout (0110 buildings,
-- 0210 building depreciation, ...), but the chart this product actually seeds
-- (internal/handler/organizations.go) is a different variant:
--   0110 Yer · 0120 Binolar/inshootlar · 0130 Mashina-uskunalar ·
--   0140 Mebel · 0150 Kompyuter · 0160 Transport
--   0220/0230/0260 depreciation leaves; 0210/0240/0250 DO NOT EXIST.
-- Result: seeded categories pointed at missing or wrong accounts ("buildings"
-- was booked to Yer!) and depreciation posting failed with ACCOUNT_NOT_FOUND.
--
-- This migration (1) adds the two genuinely missing depreciation leaves
-- (0240 mebel, 0250 kompyuter), (2) remaps ONLY still-default category rows to
-- the real chart (user-customized rows are left alone), (3) re-seeds the
-- tenant-creation trigger with the corrected layout.

-- ---------------------------------------------------------------------------
-- 1. Missing depreciation leaves per organization
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    org_record  RECORD;
    contra_type uuid;
BEGIN
    SELECT id INTO contra_type FROM account_types WHERE code = 'CONTRA_ASSET' LIMIT 1;
    IF contra_type IS NULL THEN
        RAISE NOTICE 'CONTRA_ASSET type missing, skipping 0240/0250 seed';
        RETURN;
    END IF;

    FOR org_record IN
        SELECT o.id AS org_id, o.tenant_id FROM organizations o WHERE o.deleted_at IS NULL
    LOOP
        INSERT INTO accounts (
            id, tenant_id, organization_id, account_type_id, code, name, description,
            is_bank_account, is_control_account, is_reconcilable,
            current_balance, opening_balance, is_active, created_at, updated_at
        )
        SELECT uuid_generate_v4(), org_record.tenant_id, org_record.org_id, contra_type,
               '0240', 'Mebel va ofis jihozlarining eskirishi', 'Depreciation of furniture and office equipment',
               false, false, false, 0, 0, true, NOW(), NOW()
        WHERE NOT EXISTS (SELECT 1 FROM accounts WHERE tenant_id = org_record.tenant_id
                          AND organization_id = org_record.org_id AND code = '0240' AND deleted_at IS NULL);

        INSERT INTO accounts (
            id, tenant_id, organization_id, account_type_id, code, name, description,
            is_bank_account, is_control_account, is_reconcilable,
            current_balance, opening_balance, is_active, created_at, updated_at
        )
        SELECT uuid_generate_v4(), org_record.tenant_id, org_record.org_id, contra_type,
               '0250', 'Kompyuter jihozlarining eskirishi', 'Depreciation of computer equipment',
               false, false, false, 0, 0, true, NOW(), NOW()
        WHERE NOT EXISTS (SELECT 1 FROM accounts WHERE tenant_id = org_record.tenant_id
                          AND organization_id = org_record.org_id AND code = '0250' AND deleted_at IS NULL);
    END LOOP;
END $$;

-- ---------------------------------------------------------------------------
-- 2. Remap still-default category rows to the real chart.
--    Guarded by the OLD default codes so customized mappings are untouched.
-- ---------------------------------------------------------------------------
UPDATE fa_categories SET asset_account='0120', depreciation_account='0220', updated_at=NOW()
WHERE code='buildings' AND asset_account='0110' AND COALESCE(depreciation_account,'')='0210';

UPDATE fa_categories SET asset_account='0120', depreciation_account='0220', updated_at=NOW()
WHERE code='structures' AND asset_account='0120' AND COALESCE(depreciation_account,'')='0220';

UPDATE fa_categories SET asset_account='0160', depreciation_account='0260', updated_at=NOW()
WHERE code='vehicles' AND asset_account='0140' AND COALESCE(depreciation_account,'')='0240';

UPDATE fa_categories SET asset_account='0140', depreciation_account='0240', updated_at=NOW()
WHERE code='furniture' AND asset_account='0150' AND COALESCE(depreciation_account,'')='0250';

UPDATE fa_categories SET asset_account='0150', depreciation_account='0250', updated_at=NOW()
WHERE code='computers' AND asset_account='0160' AND COALESCE(depreciation_account,'')='0260';

UPDATE fa_categories SET asset_account='0110', updated_at=NOW()
WHERE code='land' AND asset_account='0180';

-- machinery 0130/0230 was already correct.

-- ---------------------------------------------------------------------------
-- 3. Corrected tenant-creation trigger (replaces the 453 version)
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION fa_seed_tenant_defaults() RETURNS trigger AS $$
BEGIN
    INSERT INTO fa_categories (tenant_id, code, name_uz, name_ru, asset_account, depreciation_account, depreciable, default_useful_life_months)
    VALUES
        (NEW.id, 'buildings',  'Binolar va inshootlar', 'Здания и сооружения', '0120', '0220', true, 240),
        (NEW.id, 'machinery',  'Mashina va uskunalar',  'Машины и оборуд.',    '0130', '0230', true, 96),
        (NEW.id, 'vehicles',   'Transport vositalari',  'Транспорт',           '0160', '0260', true, 84),
        (NEW.id, 'furniture',  'Mebel va jihozlar',     'Мебель',              '0140', '0240', true, 60),
        (NEW.id, 'computers',  'Kompyuter texnikasi',   'Компьютеры',          '0150', '0250', true, 36),
        (NEW.id, 'land',       'Yer',                   'Земля',               '0110', NULL,   false, NULL)
    ON CONFLICT (tenant_id, code) DO NOTHING;

    INSERT INTO fa_departments (tenant_id, code, name_uz, expense_account)
    VALUES
        (NEW.id, 'production', 'Ishlab chiqarish', '2010'),
        (NEW.id, 'admin',      'Ma''muriyat',      '9420'),
        (NEW.id, 'sales',      'Sotuv',            '9410')
    ON CONFLICT (tenant_id, code) DO NOTHING;

    INSERT INTO fa_settings (tenant_id) VALUES (NEW.id) ON CONFLICT (tenant_id) DO NOTHING;
    RETURN NEW;
END $$ LANGUAGE plpgsql;
