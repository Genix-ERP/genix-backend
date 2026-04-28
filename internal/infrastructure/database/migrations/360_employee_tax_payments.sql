-- 360_employee_tax_payments.sql
--
-- Closes the loop on the employee-tax workflow.
--
-- Background. payroll_entry_taxes (migration 330) is the immutable
-- accrual table — every payroll entry writes one row per tax with the
-- snapshotted rate / base / liability account. ProcessPayroll then
-- emits one journal entry per period, debiting salary expense and
-- crediting the tax-liability accounts. So we have ACCRUAL covered.
--
-- What was missing: a way to RECORD A PAYMENT against those liabilities
-- and have the Tax Reports page show the running balance per tax
-- (accrued − paid → pending). Without it the orange "Record Payment"
-- button only worked for VAT, and the Employee Taxes tab showed totals
-- that never went down.
--
-- This migration adds the payments table + the idempotency guard on
-- ProcessPayroll so re-running it for the same period doesn't post a
-- second copy of the same journal entry.

-- ─────────────────────────────────────────────────────────────────────
-- 1. employee_tax_payments — one row per (org, tax, period) payment
-- ─────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS employee_tax_payments (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,

    -- Which tax (NDFL / INPS / Profsoyuz / Social tax). Reference is
    -- soft so the payment row survives a tax-catalog rename / archive.
    tax_id          UUID REFERENCES employee_taxes(id) ON DELETE SET NULL,
    tax_code        VARCHAR(50)  NOT NULL,
    tax_name        VARCHAR(255) NOT NULL,
    payer           VARCHAR(20)  NOT NULL, -- 'employee' | 'employer'

    -- Period this payment clears. Both inclusive. Periods are usually
    -- a calendar month but the schema keeps the date range explicit so
    -- partial / cumulative payments can also be recorded.
    period_start    DATE NOT NULL,
    period_end      DATE NOT NULL,

    amount          NUMERIC(18,2) NOT NULL CHECK (amount > 0),

    -- Books reference. The journal entry created when the payment is
    -- recorded debits the liability account and credits cash / bank.
    journal_entry_id UUID REFERENCES journal_entries(id) ON DELETE SET NULL,

    paid_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    paid_by         UUID REFERENCES users(id) ON DELETE SET NULL,
    payment_method  VARCHAR(40),     -- 'bank_transfer' | 'cash' | etc.
    note            TEXT,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_etp_tenant_org
    ON employee_tax_payments (tenant_id, organization_id);

-- The Employee Taxes tab joins on (code, period_start, period_end) to
-- compute pending = accrued − paid. The composite index covers that
-- aggregation directly.
CREATE INDEX IF NOT EXISTS idx_etp_code_period
    ON employee_tax_payments (tenant_id, tax_code, period_start, period_end)
    WHERE deleted_at IS NULL;

-- ─────────────────────────────────────────────────────────────────────
-- 2. ProcessPayroll idempotency
-- ─────────────────────────────────────────────────────────────────────
-- payroll_periods now records the journal entry it produced so
-- subsequent ProcessPayroll calls can short-circuit instead of
-- emitting a duplicate. The FK is SET NULL because deleting the
-- journal entry shouldn't cascade and erase the payroll history.
ALTER TABLE payroll_periods
    ADD COLUMN IF NOT EXISTS taxes_journal_entry_id
        UUID REFERENCES journal_entries(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_payroll_periods_taxes_je
    ON payroll_periods (taxes_journal_entry_id)
    WHERE taxes_journal_entry_id IS NOT NULL;
