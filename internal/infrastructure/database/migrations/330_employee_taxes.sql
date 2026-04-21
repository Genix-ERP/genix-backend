-- 330_employee_taxes.sql
-- Configurable employee-level taxes (replaces the hardcoded income_tax / social_security /
-- pension columns on payroll_entries, which are left in place for backward compatibility
-- but no longer driven by the payroll flow once this migration ships).
--
-- Shape:
--   employee_taxes          = per-tenant catalog of taxes the admin defines in Settings → Finance
--   payroll_entry_taxes     = immutable snapshot of taxes actually applied to each payroll entry,
--                              with rate, base, amount, and GL accounts captured at apply time.
--
-- Journal posting at ProcessPayroll time:
--   employee-paid tax  ->  Cr tax_liability_account_id (deducted from net that the employee receives)
--   employer-paid tax  ->  Dr expense_account_id, Cr tax_liability_account_id  (company cost)
--
-- See TT_Buxgalteriya_ERP.docx §payroll and TT_ish_haqi_moduli.docx for context.

-- ─────────────────────────────────────────────────────────────────────────────
-- 1. employee_taxes : tax catalog
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS employee_taxes (
    id                     UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id              UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id        UUID REFERENCES organizations(id) ON DELETE SET NULL,

    code                   VARCHAR(50) NOT NULL,
    name                   VARCHAR(255) NOT NULL,
    description            TEXT,

    -- Rate stored as percent, e.g. 12.00 = 12%
    rate                   NUMERIC(9,4) NOT NULL DEFAULT 0 CHECK (rate >= 0 AND rate <= 100),

    -- What to apply the rate to.
    --   base_salary  = payroll_entries.base_salary
    --   gross        = base + overtime_amount + bonus + allowances
    --   taxable      = gross minus any prior 'employee' taxes that run first
    base_type              VARCHAR(20) NOT NULL DEFAULT 'gross'
                             CHECK (base_type IN ('base_salary','gross','taxable')),

    -- Who ultimately bears the cost.
    --   employee = reduces the net pay the employee receives
    --   employer = company expense (does not reduce net pay, but creates expense + liability)
    payer                  VARCHAR(20) NOT NULL DEFAULT 'employee'
                             CHECK (payer IN ('employee','employer')),

    -- Liability account credited for the tax obligation (required to post to journal).
    account_id             UUID REFERENCES accounts(id) ON DELETE SET NULL,

    -- Expense account debited when payer='employer'. Ignored when payer='employee'.
    expense_account_id     UUID REFERENCES accounts(id) ON DELETE SET NULL,

    is_active              BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order             INTEGER NOT NULL DEFAULT 0,

    created_by             UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at             TIMESTAMPTZ
);

-- One code per tenant (case-insensitive), excluding soft-deleted rows
CREATE UNIQUE INDEX IF NOT EXISTS uq_employee_taxes_tenant_code
    ON employee_taxes (tenant_id, LOWER(code))
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_employee_taxes_tenant_active
    ON employee_taxes (tenant_id, is_active)
    WHERE deleted_at IS NULL;

-- ─────────────────────────────────────────────────────────────────────────────
-- 2. payroll_entry_taxes : immutable snapshot of taxes applied to each entry
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS payroll_entry_taxes (
    id                     UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id              UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id        UUID REFERENCES organizations(id) ON DELETE SET NULL,
    payroll_entry_id       UUID NOT NULL REFERENCES payroll_entries(id) ON DELETE CASCADE,

    -- Soft reference to the tax definition; snapshot fields are authoritative once written.
    tax_id                 UUID REFERENCES employee_taxes(id) ON DELETE SET NULL,

    tax_code_snapshot      VARCHAR(50)  NOT NULL,
    tax_name_snapshot      VARCHAR(255) NOT NULL,
    rate_snapshot          NUMERIC(9,4) NOT NULL,
    base_type_snapshot     VARCHAR(20)  NOT NULL,
    payer_snapshot         VARCHAR(20)  NOT NULL,

    base_amount            NUMERIC(18,2) NOT NULL DEFAULT 0,
    amount                 NUMERIC(18,2) NOT NULL DEFAULT 0,

    account_id_snapshot           UUID REFERENCES accounts(id) ON DELETE SET NULL,
    expense_account_id_snapshot   UUID REFERENCES accounts(id) ON DELETE SET NULL,

    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (payroll_entry_id, tax_id)
);

CREATE INDEX IF NOT EXISTS idx_payroll_entry_taxes_entry
    ON payroll_entry_taxes (payroll_entry_id);

CREATE INDEX IF NOT EXISTS idx_payroll_entry_taxes_tenant_created
    ON payroll_entry_taxes (tenant_id, created_at);

-- ─────────────────────────────────────────────────────────────────────────────
-- 3. Seed common Uzbekistan employee taxes (disabled by default — admin must
--    enable and pick a GL account). Per-tenant, one per existing tenant.
--
--    NDFL          = 12%  on gross, employee-paid   (Daromad solig'i)
--    INPS          = 0.1% on gross, employee-paid   (Ijtimoiy sug'urta fondiga jismoniy shaxs)
--    Social tax    = 12%  on gross, employer-paid   (Ijtimoiy soliq — ish beruvchi)
-- ─────────────────────────────────────────────────────────────────────────────
INSERT INTO employee_taxes (
    id, tenant_id, organization_id,
    code, name, description,
    rate, base_type, payer,
    is_active, sort_order,
    created_at, updated_at
)
SELECT
    uuid_generate_v4(), t.id, NULL,
    s.code, s.name, s.description,
    s.rate, s.base_type, s.payer,
    FALSE, s.sort_order,
    NOW(), NOW()
FROM tenants t
CROSS JOIN (VALUES
    ('NDFL',       'Daromad solig''i (NDFL)',
       'Jismoniy shaxslardan olinadigan daromad solig''i. 12% — brutto maoshdan ushlab qolinadi.',
       12.0000, 'gross', 'employee', 10),
    ('INPS',       'INPS (Ijtimoiy sug''urta jamg''armasi)',
       'Jismoniy shaxsning ijtimoiy sug''urta jamg''armasiga majburiy badali. Brutto maoshdan ushlab qolinadi.',
       0.1000, 'gross', 'employee', 20),
    ('SOC_TAX',    'Ijtimoiy soliq (ish beruvchi)',
       'Ish beruvchi tomonidan to''lanadigan ijtimoiy soliq. Xodim maoshidan ushlab qolinmaydi, kompaniya xarajati.',
       12.0000, 'gross', 'employer', 30)
) AS s(code, name, description, rate, base_type, payer, sort_order)
WHERE NOT EXISTS (
    SELECT 1 FROM employee_taxes et
    WHERE et.tenant_id = t.id
      AND LOWER(et.code) = LOWER(s.code)
      AND et.deleted_at IS NULL
);
