-- Tax module foundation — the "universal constructor" (genix_soliq_spec §2)
-- and the tax catalog + versioned rates (§3). Rates are NEVER hardcoded in
-- code: documents read the rate in force on their own date from tax_type_rates.
--
-- These are NEW tables and do not touch the existing (tenant-scoped, unversioned)
-- tax_rates table used by other finance code.

-- §3: national tax catalog. Global reference data (not tenant-scoped).
CREATE TABLE IF NOT EXISTS tax_types (
    code              text PRIMARY KEY,               -- 'vat'|'profit'|'turnover'|'pit'|'inps'|'social'|'property'|'land'|'water'|'dividend'|'union'
    name_uz           text NOT NULL,
    name_ru           text NOT NULL,
    kind              text NOT NULL DEFAULT 'tax',     -- 'tax' | 'deduction' (union → deduction, not in tax reporting)
    category          text NOT NULL DEFAULT 'optional',-- 'mandatory' | 'regime' | 'optional' (§2 three categories)
    mandatory         boolean NOT NULL DEFAULT false,  -- true: pit,inps,social — cannot disable while active employees exist
    liability_account text,                            -- 6410 (budget) | 6520 (social,inps) | 6990 (union)
    analytic_code     text,                            -- analytic of the tax kind on 6410 (§11 p.4)
    sort_order        int  NOT NULL DEFAULT 100,
    created_at        timestamptz NOT NULL DEFAULT now()
);

-- §3: versioned national rates. A document takes the rate valid on its date.
CREATE TABLE IF NOT EXISTS tax_type_rates (
    id           uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
    tax_code     text NOT NULL REFERENCES tax_types(code) ON DELETE CASCADE,
    rate_variant text NOT NULL DEFAULT 'standard',     -- 'standard'|'budget'|'nonresident'…
    rate         numeric(6,3) NOT NULL,                -- e.g. 12.000
    valid_from   date NOT NULL,
    valid_to     date,                                 -- NULL = in force
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tax_code, rate_variant, valid_from)
);

-- §2: per-tenant on/off toggle for each tax/payment, effective from a date.
CREATE TABLE IF NOT EXISTS tenant_taxes (
    tenant_id    uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    tax_code     text NOT NULL REFERENCES tax_types(code),
    enabled      boolean NOT NULL,
    valid_from   date NOT NULL,
    valid_to     date,
    rate_variant text NOT NULL DEFAULT 'standard',
    updated_by   uuid,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, tax_code, valid_from)
);

-- §2: tenant tax regime with effective date; transition history is kept.
CREATE TABLE IF NOT EXISTS tenant_tax_regime (
    id         uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    regime     text NOT NULL,                          -- 'turnover' | 'vat_profit'
    valid_from date NOT NULL,
    created_by uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, valid_from)
);

CREATE INDEX IF NOT EXISTS idx_tax_type_rates_lookup ON tax_type_rates (tax_code, valid_from DESC);
CREATE INDEX IF NOT EXISTS idx_tenant_taxes_lookup   ON tenant_taxes (tenant_id, tax_code, valid_from DESC);

-- ---- Seed catalog (§3) ----------------------------------------------------
INSERT INTO tax_types (code, name_uz, name_ru, kind, category, mandatory, liability_account, analytic_code, sort_order) VALUES
 ('vat',      'QQS',                   'НДС',                'tax',      'regime',    false, '6410', 'vat',      10),
 ('profit',   'Foyda solig''i',        'Налог на прибыль',   'tax',      'regime',    false, '6410', 'profit',   20),
 ('turnover', 'Aylanmadan solig''i',   'Налог с оборота',    'tax',      'regime',    false, '6410', 'turnover', 30),
 ('pit',      'JSHDS',                 'НДФЛ',               'tax',      'mandatory', true,  '6410', 'pit',      40),
 ('inps',     'INPS',                  'ИНПС',               'tax',      'mandatory', true,  '6520', 'inps',     50),
 ('social',   'Ijtimoiy soliq',        'Социальный налог',   'tax',      'mandatory', true,  '6520', 'social',   60),
 ('property', 'Mol-mulk solig''i',     'Налог на имущество', 'tax',      'optional',  false, '6410', 'property', 70),
 ('land',     'Yer solig''i',          'Земельный налог',    'tax',      'optional',  false, '6410', 'land',     80),
 ('water',    'Suv solig''i',          'Налог за воду',      'tax',      'optional',  false, '6410', 'water',    90),
 ('dividend', 'Dividend solig''i',     'Налог с дивидендов', 'tax',      'optional',  false, '6410', 'dividend', 100),
 ('union',    'Kasaba uyushma badali', 'Профсоюзные взносы', 'deduction','optional',  false, '6990', 'union',    110)
ON CONFLICT (code) DO NOTHING;

-- ---- Seed 2026 rates (§3) --------------------------------------------------
-- Second variants: PIT nonresident 20%, social for budget orgs 25%.
-- land/water have no flat % (computed per NK sums) → entered on the accrual doc.
INSERT INTO tax_type_rates (tax_code, rate_variant, rate, valid_from) VALUES
 ('vat',      'standard',    12.000, '2026-01-01'),
 ('profit',   'standard',    15.000, '2026-01-01'),
 ('turnover', 'standard',     4.000, '2026-01-01'),
 ('pit',      'standard',    12.000, '2026-01-01'),
 ('pit',      'nonresident', 20.000, '2026-01-01'),
 ('inps',     'standard',     0.100, '2026-01-01'),
 ('social',   'standard',    12.000, '2026-01-01'),
 ('social',   'budget',      25.000, '2026-01-01'),
 ('property', 'standard',     1.500, '2026-01-01'),
 ('dividend', 'standard',     5.000, '2026-01-01'),
 ('union',    'standard',     1.000, '2026-01-01')
ON CONFLICT (tax_code, rate_variant, valid_from) DO NOTHING;
