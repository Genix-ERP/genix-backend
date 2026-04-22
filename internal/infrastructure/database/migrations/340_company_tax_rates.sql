-- 340_company_tax_rates.sql
-- Seed the four remaining DEFAULT taxes from §1.2 of
-- ТЗ_Ish_Haqi_Soliq_Tolik.docx. The TZ lists 8 taxes total; the first
-- four are payroll-scoped and already live in employee_taxes:
--
--     ┌──────────────────────────────────────────────────────────────┐
--     │ #  Tax                        TZ §1.2 rate   Where seeded    │
--     ├──────────────────────────────────────────────────────────────┤
--     │ 1  НДФЛ (Daromad solig'i)     12%            employee_taxes  │ migration 330
--     │ 2  Профсоюз (Kasaba bad.)     1%             employee_taxes  │ migration 338
--     │ 3  ИНПС (pensiya jamg'.)      0.1%           employee_taxes  │ migration 330
--     │ 4  ЕСП / SOC_TAX              12%            employee_taxes  │ migration 330
--     │ 5  НДС                        12%            company_tax_rates (this) │
--     │ 6  Фойда солиғи               15%            company_tax_rates (this) │
--     │ 7  Айланма солиғи             4%             company_tax_rates (this) │
--     │ 8  Дивиденд солиғи            5%             company_tax_rates (this) │
--     └──────────────────────────────────────────────────────────────┘
--
-- Taxes 5–8 are company-level activity taxes — not tied to a single
-- payroll entry — so they get their own catalog. `applies_to` categorises
-- what economic event the tax is levied on so the UI / reports can pick
-- the right rate without string-matching on the code.
--
-- Disabled (is_active = FALSE) by default, same pattern as migrations
-- 330/338 for employee_taxes: admin enables from Settings → Finance and
-- optionally pins an account_id before any journal posting kicks in.

-- ─────────────────────────────────────────────────────────────────────
-- 1. company_tax_rates : per-tenant catalog of activity-level taxes
-- ─────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS company_tax_rates (
    id                 UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id          UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id    UUID REFERENCES organizations(id) ON DELETE SET NULL,

    code               VARCHAR(50)  NOT NULL,
    name               VARCHAR(255) NOT NULL,
    description        TEXT,

    -- Rate stored as percent, e.g. 12.00 = 12%.
    rate               NUMERIC(9,4) NOT NULL DEFAULT 0
                         CHECK (rate >= 0 AND rate <= 100),

    -- What the rate is applied to:
    --   sales    = applied to sales turnover (НДС on realisation).
    --   profit   = applied to the profit-tax base (see profit_tax_calc).
    --   turnover = applied to total revenue for non-VAT simplified payers.
    --   dividend = applied to dividend distributions.
    --   other    = escape hatch for tenant-defined company taxes.
    applies_to         VARCHAR(20) NOT NULL
                         CHECK (applies_to IN ('sales','profit','turnover','dividend','other')),

    -- Liability account credited when the tax is posted. NULL until the
    -- admin picks one in Settings → Finance, matching employee_taxes.
    account_id         UUID REFERENCES accounts(id) ON DELETE SET NULL,

    is_active          BOOLEAN NOT NULL DEFAULT FALSE,
    sort_order         INTEGER NOT NULL DEFAULT 0,

    created_by         UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at         TIMESTAMPTZ
);

-- One code per tenant (case-insensitive), excluding soft-deleted rows.
CREATE UNIQUE INDEX IF NOT EXISTS uq_company_tax_rates_tenant_code
    ON company_tax_rates (tenant_id, LOWER(code))
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_company_tax_rates_tenant_active
    ON company_tax_rates (tenant_id, is_active)
    WHERE deleted_at IS NULL;

-- ─────────────────────────────────────────────────────────────────────
-- 2. Seed the four TZ §1.2 company-level taxes per existing tenant.
--    Idempotent: WHERE NOT EXISTS guard so re-running the migration on
--    partially-seeded tenants leaves their data alone.
-- ─────────────────────────────────────────────────────────────────────
INSERT INTO company_tax_rates (
    id, tenant_id, organization_id,
    code, name, description,
    rate, applies_to,
    is_active, sort_order,
    created_at, updated_at
)
SELECT
    uuid_generate_v4(), t.id, NULL,
    s.code, s.name, s.description,
    s.rate, s.applies_to,
    FALSE, s.sort_order,
    NOW(), NOW()
FROM tenants t
CROSS JOIN (VALUES
    ('NDS',         'QQS (NDS) — Qo''shilgan qiymat solig''i',
       'Sotuv (realizatsiya) aylanmasidan 12% QQS. Kirish QQS (zachyot) hisobga olinadi, sof to''lov = realizatsiya QQS − zachyot QQS.',
       12.0000, 'sales',    10),
    ('PROFIT_TAX',  'Foyda solig''i',
       'Soliq bazasidan (daromad − tan olingan xarajatlar) 15%. Tan olinmagan xarajatlar bazadan chiqarilmaydi.',
       15.0000, 'profit',   20),
    ('TURNOVER',    'Aylanma solig''i',
       'QQS to''lovchi bo''lmagan kompaniyalar uchun umumiy sotuv aylanmasidan 4%. QQS bilan bir vaqtda qo''llanmaydi.',
        4.0000, 'turnover', 30),
    ('DIVIDEND',    'Dividend solig''i',
       'Ta''sischilarga taqsimlangan foydadan 5%.',
        5.0000, 'dividend', 40)
) AS s(code, name, description, rate, applies_to, sort_order)
WHERE NOT EXISTS (
    SELECT 1 FROM company_tax_rates ctr
    WHERE ctr.tenant_id = t.id
      AND LOWER(ctr.code) = LOWER(s.code)
      AND ctr.deleted_at IS NULL
);
