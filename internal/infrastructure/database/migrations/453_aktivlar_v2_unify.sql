-- 453_aktivlar_v2_unify.sql
--
-- Aktivlar (Fixed Assets) unification — audit 2026-08-03 (docs/aktivlar-audit.md).
--
-- 1) fa_assets gains the operational references the legacy register had or the
--    audit found missing: responsible employee (moddiy javobgarlik), construction
--    object, source purchase order, sale fields, and a legacy_id marker that makes
--    the data migration below idempotent and traceable.
-- 2) fa_settings gains the six lifecycle accounts that were hardcoded in
--    fa_assets.go (0820/6010/5010/5110/4410/9210) plus the disposal result
--    accounts — NSBU defaults, tenant-editable in Settings.
-- 3) fa_categories gains default_useful_life_months so the unified form can
--    prefill the term from the category.
-- 4) Legacy fixed_assets rows are migrated into fa_assets; accumulated
--    depreciation is preserved as ONE opening fa_depreciation_entries row
--    (run_id NULL, no journal entry — the GL already carries the legacy
--    postings). Legacy tables are left in place read-only; their routes and the
--    legacy cron are removed in code.

-- ---------------------------------------------------------------------------
-- 1. fa_assets: operational references + sale fields + migration marker
-- ---------------------------------------------------------------------------
ALTER TABLE fa_assets ADD COLUMN IF NOT EXISTS assigned_employee_id   UUID REFERENCES employees(id) ON DELETE SET NULL;
-- construction_projects.id is BIGSERIAL (111_construction_module.sql), not UUID.
ALTER TABLE fa_assets ADD COLUMN IF NOT EXISTS construction_object_id BIGINT REFERENCES construction_projects(id) ON DELETE SET NULL;
ALTER TABLE fa_assets ADD COLUMN IF NOT EXISTS purchase_order_id      UUID REFERENCES purchase_orders(id) ON DELETE SET NULL;
ALTER TABLE fa_assets ADD COLUMN IF NOT EXISTS disposal_amount        NUMERIC(18,2);
ALTER TABLE fa_assets ADD COLUMN IF NOT EXISTS disposal_reason        TEXT;
ALTER TABLE fa_assets ADD COLUMN IF NOT EXISTS legacy_id              UUID;

CREATE UNIQUE INDEX IF NOT EXISTS uq_fa_assets_legacy ON fa_assets(legacy_id) WHERE legacy_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_fa_assets_employee ON fa_assets(assigned_employee_id) WHERE assigned_employee_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_fa_assets_object   ON fa_assets(construction_object_id) WHERE construction_object_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- 2. fa_settings: tenant-editable lifecycle accounts (NSBU defaults)
-- ---------------------------------------------------------------------------
ALTER TABLE fa_settings ADD COLUMN IF NOT EXISTS acquisition_account         TEXT NOT NULL DEFAULT '0820'; -- kapital qo'yilma
ALTER TABLE fa_settings ADD COLUMN IF NOT EXISTS ap_account                  TEXT NOT NULL DEFAULT '6010'; -- ta'minotchilar
ALTER TABLE fa_settings ADD COLUMN IF NOT EXISTS cash_account                TEXT NOT NULL DEFAULT '5010'; -- kassa
ALTER TABLE fa_settings ADD COLUMN IF NOT EXISTS bank_account                TEXT NOT NULL DEFAULT '5110'; -- hisob-kitob schoti
ALTER TABLE fa_settings ADD COLUMN IF NOT EXISTS vat_input_account           TEXT NOT NULL DEFAULT '4410'; -- QQS hisobga olish
ALTER TABLE fa_settings ADD COLUMN IF NOT EXISTS disposal_account            TEXT NOT NULL DEFAULT '9210'; -- chiqib ketish (tranzit)
ALTER TABLE fa_settings ADD COLUMN IF NOT EXISTS disposal_gain_account       TEXT NOT NULL DEFAULT '9310'; -- chiqib ketishdan foyda
ALTER TABLE fa_settings ADD COLUMN IF NOT EXISTS disposal_loss_account       TEXT NOT NULL DEFAULT '9430'; -- boshqa operatsion xarajatlar
ALTER TABLE fa_settings ADD COLUMN IF NOT EXISTS disposal_receivable_account TEXT NOT NULL DEFAULT '4890'; -- boshqa debitorlar (sotish)

-- ---------------------------------------------------------------------------
-- 3. fa_categories: default useful life for the unified form
-- ---------------------------------------------------------------------------
ALTER TABLE fa_categories ADD COLUMN IF NOT EXISTS default_useful_life_months INTEGER;

UPDATE fa_categories SET default_useful_life_months = d.months
FROM (VALUES
    ('buildings', 240), ('structures', 180), ('machinery', 96), ('vehicles', 84),
    ('furniture', 60), ('computers', 36)
) AS d(code, months)
WHERE fa_categories.code = d.code AND fa_categories.default_useful_life_months IS NULL;

-- Keep the tenant-creation trigger in sync with the new column.
CREATE OR REPLACE FUNCTION fa_seed_tenant_defaults() RETURNS trigger AS $$
BEGIN
    INSERT INTO fa_categories (tenant_id, code, name_uz, name_ru, asset_account, depreciation_account, depreciable, default_useful_life_months)
    VALUES
        (NEW.id, 'buildings',  'Binolar',              'Здания',            '0110', '0210', true, 240),
        (NEW.id, 'structures', 'Inshootlar',           'Сооружения',        '0120', '0220', true, 180),
        (NEW.id, 'machinery',  'Mashina va uskunalar', 'Машины и оборуд.',  '0130', '0230', true, 96),
        (NEW.id, 'vehicles',   'Transport vositalari', 'Транспорт',         '0140', '0240', true, 84),
        (NEW.id, 'furniture',  'Mebel va jihozlar',    'Мебель',            '0150', '0250', true, 60),
        (NEW.id, 'computers',  'Kompyuter texnikasi',  'Компьютеры',        '0160', '0260', true, 36),
        (NEW.id, 'land',       'Yer',                  'Земля',             '0180', NULL,   false, NULL)
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

-- ---------------------------------------------------------------------------
-- 4. Legacy fixed_assets -> fa_assets data migration (idempotent via legacy_id)
--
-- Mapping decisions (documented in docs/aktivlar-audit.md §13):
--   category_name/asset form values -> fa_categories.code
--     (equipment->machinery; unknown/intangible/other -> machinery)
--   department: legacy had none -> 'admin' cost center
--   status: active/under_maintenance -> in_service (commissioning = acquisition
--           date; legacy accrued from acquisition month, closest equivalent),
--           anything else -> disposed
--   custodian_id -> assigned_employee_id
--   Rows violating v2 CHECKs are clamped (salvage) or skipped (cost <= 0).
--   Inventory number: legacy asset_code, suffixed '-M' on collision.
-- ---------------------------------------------------------------------------
INSERT INTO fa_assets (
    id, tenant_id, organization_id, inventory_number, name, category_id, department_id,
    serial_number, location, purchase_date, commissioning_date, disposal_date,
    cost, salvage_value, useful_life_months, method, status, accumulated_depreciation,
    supplier_id, payment_method, doc_number, doc_date, notes,
    assigned_employee_id, disposal_amount, disposal_reason, legacy_id,
    created_by, created_at, updated_at, deleted_at
)
SELECT
    uuid_generate_v4(),
    f.tenant_id,
    f.organization_id,
    CASE WHEN EXISTS (SELECT 1 FROM fa_assets x WHERE x.tenant_id = f.tenant_id AND x.inventory_number = f.asset_code)
         THEN f.asset_code || '-M' ELSE f.asset_code END,
    f.name,
    c.id,
    d.id,
    f.serial_number,
    f.location,
    f.acquisition_date,
    CASE WHEN COALESCE(f.status, 'active') IN ('active', 'under_maintenance') OR f.disposal_date IS NOT NULL
         THEN f.acquisition_date END,
    f.disposal_date,
    f.acquisition_cost,
    CASE WHEN COALESCE(f.salvage_value, 0) < 0 OR COALESCE(f.salvage_value, 0) >= f.acquisition_cost
         THEN 0 ELSE COALESCE(f.salvage_value, 0) END,
    GREATEST(COALESCE(f.useful_life_months, 60), 1),
    CASE WHEN f.depreciation_method IN ('straight_line', 'declining_balance', 'double_declining')
         THEN f.depreciation_method ELSE 'straight_line' END,
    CASE WHEN COALESCE(f.status, 'active') IN ('active', 'under_maintenance') THEN 'in_service'::fa_asset_status
         ELSE 'disposed'::fa_asset_status END,
    LEAST(COALESCE(f.accumulated_depreciation, 0),
          f.acquisition_cost - CASE WHEN COALESCE(f.salvage_value, 0) < 0 OR COALESCE(f.salvage_value, 0) >= f.acquisition_cost
                                    THEN 0 ELSE COALESCE(f.salvage_value, 0) END),
    f.supplier_id,
    NULLIF(f.payment_method, ''),
    f.document_number,
    f.document_date,
    TRIM(BOTH E'\n' FROM COALESCE(f.description, '') ||
        CASE WHEN f.disposal_reason IS NOT NULL THEN E'\nChiqarish sababi (legacy): ' || f.disposal_reason ELSE '' END),
    f.custodian_id,
    f.disposal_amount,
    f.disposal_reason,
    f.id,
    f.created_by, f.created_at, f.updated_at, f.deleted_at
FROM fixed_assets f
JOIN fa_categories c
  ON c.tenant_id = f.tenant_id
 AND c.code = CASE lower(COALESCE(f.category_name, 'equipment'))
                  WHEN 'buildings' THEN 'buildings'
                  WHEN 'vehicles'  THEN 'vehicles'
                  WHEN 'computers' THEN 'computers'
                  WHEN 'furniture' THEN 'furniture'
                  ELSE 'machinery'
              END
JOIN fa_departments d ON d.tenant_id = f.tenant_id AND d.code = 'admin'
WHERE f.acquisition_cost > 0
  AND NOT EXISTS (SELECT 1 FROM fa_assets m WHERE m.legacy_id = f.id);

-- Opening depreciation entry: preserve legacy accumulated as one 'active' row so
-- future runs continue from the correct base. No journal entry — the GL already
-- reflects legacy postings. Period = last legacy depreciation month (or the
-- acquisition month when none was ever run).
INSERT INTO fa_depreciation_entries (tenant_id, run_id, asset_id, period, amount, debit_account, credit_account, status)
SELECT a.tenant_id, NULL, a.id,
       to_char(COALESCE(f.last_depr_date, f.acquisition_date), 'YYYY-MM'),
       a.accumulated_depreciation,
       d.expense_account,
       COALESCE(c.depreciation_account, '0230'),
       'active'
FROM fa_assets a
JOIN fixed_assets f   ON f.id = a.legacy_id
JOIN fa_categories c  ON c.id = a.category_id
JOIN fa_departments d ON d.id = a.department_id
WHERE a.legacy_id IS NOT NULL
  AND a.accumulated_depreciation > 0
  AND NOT EXISTS (SELECT 1 FROM fa_depreciation_entries e
                  WHERE e.asset_id = a.id AND e.status = 'active');
