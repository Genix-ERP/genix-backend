-- 437_fixed_assets_v2.sql
--
-- Fixed Assets + automatic depreciation module v1 (TZ v1.0, НСБУ №21/№5).
--
-- Greenfield rebuild on namespaced fa_* tables. The legacy fixed_assets /
-- asset_categories / depreciation_entries tables are left untouched (deprecated);
-- migrating historical assets with accumulated depreciation is explicitly out of
-- v1 scope (TZ §13). Account references are stored as TEXT codes (e.g. '0130')
-- snapshotted at posting time and resolved to the tenant's account UUID when a
-- journal entry is written.

-- ---------------------------------------------------------------------------
-- Asset status lifecycle: draft -> in_service -> conserved -> disposed (§4)
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'fa_asset_status') THEN
        CREATE TYPE fa_asset_status AS ENUM ('draft', 'in_service', 'conserved', 'disposed');
    END IF;
END $$;

-- ---------------------------------------------------------------------------
-- Categories ("types") — carry asset + depreciation account codes (§3)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS fa_categories (
    id                   UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id            UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    code                 TEXT NOT NULL,
    name_uz              TEXT NOT NULL,
    name_ru              TEXT,
    asset_account        TEXT NOT NULL,          -- e.g. '0130'
    depreciation_account TEXT,                   -- e.g. '0230'; NULL when depreciable=false
    depreciable          BOOLEAN NOT NULL DEFAULT true,
    is_active            BOOLEAN NOT NULL DEFAULT true,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, code),
    CHECK (NOT depreciable OR depreciation_account IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS idx_fa_categories_tenant ON fa_categories(tenant_id);

-- ---------------------------------------------------------------------------
-- Asset cost centers ("departments") — carry the expense account code (§3)
-- Kept separate from the org-structure `departments` table on purpose.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS fa_departments (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    code            TEXT NOT NULL,
    name_uz         TEXT NOT NULL,
    expense_account TEXT NOT NULL,               -- '2010' | '9410' | '9420' | ...
    is_active       BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, code)
);
CREATE INDEX IF NOT EXISTS idx_fa_departments_tenant ON fa_departments(tenant_id);

-- ---------------------------------------------------------------------------
-- Assets (§3)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS fa_assets (
    id                            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id                     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id               UUID REFERENCES organizations(id) ON DELETE SET NULL,
    inventory_number              TEXT NOT NULL,
    name                          TEXT NOT NULL,
    category_id                   UUID NOT NULL REFERENCES fa_categories(id),
    department_id                 UUID NOT NULL REFERENCES fa_departments(id),
    serial_number                 TEXT,
    location                      TEXT,
    purchase_date                 DATE NOT NULL,
    commissioning_date            DATE,                       -- NULL while draft
    disposal_date                 DATE,
    cost                          NUMERIC(18,2) NOT NULL CHECK (cost > 0),
    salvage_value                 NUMERIC(18,2) NOT NULL DEFAULT 0
                                  CHECK (salvage_value >= 0 AND salvage_value < cost),
    useful_life_months            INTEGER NOT NULL CHECK (useful_life_months > 0),
    method                        TEXT NOT NULL DEFAULT 'straight_line',
    status                        fa_asset_status NOT NULL DEFAULT 'draft',
    accumulated_depreciation      NUMERIC(18,2) NOT NULL DEFAULT 0,  -- denormalized; SUM(entries) is source of truth
    supplier_id                   UUID,
    payment_method                TEXT,                       -- 'cash' | 'credit'
    doc_number                    TEXT,
    doc_date                      DATE,
    -- per-asset account overrides (§2.6); effective = COALESCE(override, mapping)
    asset_account_override        TEXT,
    depreciation_account_override TEXT,
    expense_account_override      TEXT,
    notes                         TEXT,
    created_by                    UUID REFERENCES users(id),
    created_at                    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at                    TIMESTAMPTZ,
    UNIQUE (tenant_id, inventory_number)
);
CREATE INDEX IF NOT EXISTS idx_fa_assets_tenant   ON fa_assets(tenant_id);
CREATE INDEX IF NOT EXISTS idx_fa_assets_status   ON fa_assets(tenant_id, status);
CREATE INDEX IF NOT EXISTS idx_fa_assets_category ON fa_assets(category_id);
CREATE INDEX IF NOT EXISTS idx_fa_assets_dept     ON fa_assets(department_id);

-- ---------------------------------------------------------------------------
-- Monthly depreciation runs (draft -> posted -> reversed) (§3, §7)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS fa_depreciation_runs (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    period      CHAR(7) NOT NULL,                     -- 'YYYY-MM'
    status      TEXT NOT NULL DEFAULT 'draft',        -- 'draft' | 'posted' | 'reversed'
    skipped     JSONB NOT NULL DEFAULT '[]',          -- [{asset_id, inventory_number, reason}]
    journal_entry_id UUID,
    reversal_journal_entry_id UUID,
    created_by  UUID,
    posted_by   UUID,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    posted_at   TIMESTAMPTZ,
    reversed_at TIMESTAMPTZ,
    UNIQUE (tenant_id, period)                        -- one run per period
);
CREATE INDEX IF NOT EXISTS idx_fa_runs_tenant ON fa_depreciation_runs(tenant_id);

-- ---------------------------------------------------------------------------
-- Depreciation entries — snapshot accounts, double-post protection (§3, §8)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS fa_depreciation_entries (
    id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id        UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    run_id           UUID REFERENCES fa_depreciation_runs(id) ON DELETE CASCADE,  -- NULL for disposal top-up
    asset_id         UUID NOT NULL REFERENCES fa_assets(id) ON DELETE CASCADE,
    period           CHAR(7) NOT NULL,
    amount           NUMERIC(18,2) NOT NULL CHECK (amount > 0),
    debit_account    TEXT NOT NULL,                   -- snapshot at accrual time
    credit_account   TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'active',  -- 'active' | 'reversed'
    journal_entry_id UUID,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- Double-accrual protection: at most ONE non-reversed entry per (asset, period).
-- Reversed rows are kept (audit) but free the key so the period can be re-run (§7).
CREATE UNIQUE INDEX IF NOT EXISTS uq_fa_entries_asset_period_active
    ON fa_depreciation_entries (tenant_id, asset_id, period)
    WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_fa_entries_asset ON fa_depreciation_entries(asset_id);
CREATE INDEX IF NOT EXISTS idx_fa_entries_run   ON fa_depreciation_entries(run_id);

-- ---------------------------------------------------------------------------
-- Per-tenant module settings (§10)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS fa_settings (
    tenant_id    UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    auto_post    BOOLEAN NOT NULL DEFAULT false,
    cron_enabled BOOLEAN NOT NULL DEFAULT true,
    start_rule   TEXT NOT NULL DEFAULT 'next_month',   -- reserved: 'from_commissioning'
    rounding     INTEGER NOT NULL DEFAULT 2,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- Inventory-number counter (per tenant) — reliable under concurrency
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS fa_number_counters (
    tenant_id      UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    next_inventory BIGINT NOT NULL DEFAULT 1
);

-- ---------------------------------------------------------------------------
-- Permissions (§8.5, §8.13, §2.5, §2.6)
-- ---------------------------------------------------------------------------
INSERT INTO permissions (module, resource, action, description) VALUES
    ('accounting', 'asset_mapping', 'edit',     'Edit fixed-asset account mapping (categories/departments)'),
    ('accounting', 'asset_accounts', 'override', 'Override GL accounts on an individual asset'),
    ('accounting', 'depreciation', 'manual',    'Post manual journal entries against depreciation accounts')
ON CONFLICT (module, resource, action) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Seed defaults for EXISTING tenants (asset_account_mapping.json equivalent).
-- Codes follow the NSBU №21 chart; the client edits them in Settings (§2.5).
-- Safe to re-run: ON CONFLICT DO NOTHING.
-- ---------------------------------------------------------------------------
INSERT INTO fa_categories (tenant_id, code, name_uz, name_ru, asset_account, depreciation_account, depreciable)
SELECT t.id, c.code, c.name_uz, c.name_ru, c.asset_account, c.depreciation_account, c.depreciable
FROM tenants t
CROSS JOIN (VALUES
    ('buildings',  'Binolar',                 'Здания',            '0110', '0210', true),
    ('structures', 'Inshootlar',              'Сооружения',        '0120', '0220', true),
    ('machinery',  'Mashina va uskunalar',    'Машины и оборуд.',  '0130', '0230', true),
    ('vehicles',   'Transport vositalari',    'Транспорт',         '0140', '0240', true),
    ('furniture',  'Mebel va jihozlar',       'Мебель',            '0150', '0250', true),
    ('computers',  'Kompyuter texnikasi',     'Компьютеры',        '0160', '0260', true),
    ('land',       'Yer',                     'Земля',             '0180', NULL,   false)
) AS c(code, name_uz, name_ru, asset_account, depreciation_account, depreciable)
ON CONFLICT (tenant_id, code) DO NOTHING;

INSERT INTO fa_departments (tenant_id, code, name_uz, expense_account)
SELECT t.id, d.code, d.name_uz, d.expense_account
FROM tenants t
CROSS JOIN (VALUES
    ('production', 'Ishlab chiqarish',   '2010'),
    ('admin',      'Ma''muriyat',        '9420'),
    ('sales',      'Sotuv',              '9410')
) AS d(code, name_uz, expense_account)
ON CONFLICT (tenant_id, code) DO NOTHING;

INSERT INTO fa_settings (tenant_id)
SELECT id FROM tenants
ON CONFLICT (tenant_id) DO NOTHING;

-- Seed the same defaults automatically for FUTURE tenants — one trigger covers
-- every tenant-creation path (register, Google sign-up, admin create, …).
CREATE OR REPLACE FUNCTION fa_seed_tenant_defaults() RETURNS trigger AS $$
BEGIN
    INSERT INTO fa_categories (tenant_id, code, name_uz, name_ru, asset_account, depreciation_account, depreciable)
    VALUES
        (NEW.id, 'buildings',  'Binolar',              'Здания',            '0110', '0210', true),
        (NEW.id, 'structures', 'Inshootlar',           'Сооружения',        '0120', '0220', true),
        (NEW.id, 'machinery',  'Mashina va uskunalar', 'Машины и оборуд.',  '0130', '0230', true),
        (NEW.id, 'vehicles',   'Transport vositalari', 'Транспорт',         '0140', '0240', true),
        (NEW.id, 'furniture',  'Mebel va jihozlar',    'Мебель',            '0150', '0250', true),
        (NEW.id, 'computers',  'Kompyuter texnikasi',  'Компьютеры',        '0160', '0260', true),
        (NEW.id, 'land',       'Yer',                  'Земля',             '0180', NULL,   false)
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

DROP TRIGGER IF EXISTS trg_fa_seed_tenant ON tenants;
CREATE TRIGGER trg_fa_seed_tenant AFTER INSERT ON tenants
    FOR EACH ROW EXECUTE FUNCTION fa_seed_tenant_defaults();
