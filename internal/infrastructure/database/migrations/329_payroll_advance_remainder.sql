-- Migration 329: Payroll "Ish haqi" TT v1.1 compliance
-- Reference: /files/TT_ish_haqi_moduli.docx
--
-- Extends the existing rich payroll model (gross/tax/net/loan) with the
-- simpler advance + remainder + day-of-month payment tracking required by
-- the TT. The new columns coexist with the existing ones — callers using
-- the comprehensive model stay unchanged; the TT "Qaydnoma" flow reads and
-- writes only the new fields.
--
-- Also adds a tenant-wide payroll_settings table for the configurable
-- advance percent, currency, and company name (TT §2.2, §2.7 print header).

-- ============================================
-- 1. Per-entry advance / remainder snapshots (TT §2.3.2 / §2.4)
-- ============================================
ALTER TABLE payroll_entries
    ADD COLUMN IF NOT EXISTS advance_amount       DECIMAL(20,4) DEFAULT 0,
    ADD COLUMN IF NOT EXISTS remainder_amount     DECIMAL(20,4) DEFAULT 0,
    ADD COLUMN IF NOT EXISTS advance_paid         BOOLEAN       DEFAULT false,
    ADD COLUMN IF NOT EXISTS advance_paid_day     SMALLINT,
    ADD COLUMN IF NOT EXISTS remainder_paid       BOOLEAN       DEFAULT false,
    ADD COLUMN IF NOT EXISTS remainder_paid_day   SMALLINT,
    -- TT §2.1 immutability: snapshot of position at creation time so employee
    -- updates don't mutate historical payroll rows. employee_name is already
    -- snapshotted in this table; we add position alongside it.
    ADD COLUMN IF NOT EXISTS position_snapshot    VARCHAR(255),
    -- TT §2.3.2: snapshot of the advance percent used when this entry was
    -- created (keeps the math recomputable for legacy payrolls even if
    -- the tenant's advance_percent setting changes).
    ADD COLUMN IF NOT EXISTS advance_percent_used DECIMAL(5,2)  DEFAULT 40;

-- Day range guard (TT §2.4)
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                   WHERE conname = 'chk_payroll_entries_advance_day_range') THEN
        ALTER TABLE payroll_entries
            ADD CONSTRAINT chk_payroll_entries_advance_day_range
            CHECK (advance_paid_day IS NULL OR (advance_paid_day BETWEEN 1 AND 31));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                   WHERE conname = 'chk_payroll_entries_remainder_day_range') THEN
        ALTER TABLE payroll_entries
            ADD CONSTRAINT chk_payroll_entries_remainder_day_range
            CHECK (remainder_paid_day IS NULL OR (remainder_paid_day BETWEEN 1 AND 31));
    END IF;
END $$;

-- ============================================
-- 2. Tenant payroll settings (TT §2.2)
-- ============================================
CREATE TABLE IF NOT EXISTS payroll_settings (
    tenant_id         UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id   UUID REFERENCES organizations(id) ON DELETE CASCADE,
    advance_percent   DECIMAL(5,2) NOT NULL DEFAULT 40,
    currency          VARCHAR(16)  NOT NULL DEFAULT 'so''m',
    company_name      VARCHAR(255) NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_payroll_settings_advance_percent_range
        CHECK (advance_percent >= 0 AND advance_percent <= 100)
);

-- Per-organization overrides (optional). If a row exists with both tenant_id
-- and organization_id, it takes precedence over the tenant-wide default.
CREATE TABLE IF NOT EXISTS payroll_settings_overrides (
    id                BIGSERIAL PRIMARY KEY,
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id   UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    advance_percent   DECIMAL(5,2),
    currency          VARCHAR(16),
    company_name      VARCHAR(255),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, organization_id)
);

-- ============================================
-- 3. Backfill: compute advance/remainder snapshots for existing entries
-- ============================================
-- For rows where net_salary > 0 but advance_amount is still 0, infer the
-- advance from the default 40% of base_salary (matches the frontend's
-- hardcoded percent). This keeps the print "Day" columns meaningful for
-- pre-migration data; the day values stay NULL until the user marks them.
UPDATE payroll_entries
SET advance_amount = COALESCE(ROUND(base_salary * 0.40), 0),
    remainder_amount = COALESCE(base_salary - ROUND(base_salary * 0.40), 0),
    advance_percent_used = 40
WHERE (advance_amount IS NULL OR advance_amount = 0)
  AND base_salary IS NOT NULL AND base_salary > 0;

-- Backfill position snapshot from the employee record at migration time.
-- This is a one-time snapshot; further employee changes won't touch this row.
UPDATE payroll_entries pe
SET position_snapshot = COALESCE(e.job_title, '')
FROM employees e
WHERE pe.employee_id = e.id
  AND (pe.position_snapshot IS NULL OR pe.position_snapshot = '');

-- ============================================
-- 4. Comments
-- ============================================
COMMENT ON COLUMN payroll_entries.advance_amount IS
    'TT §2.3.2 snapshot: advance portion of salary (base_salary × advance_percent_used / 100, rounded).';
COMMENT ON COLUMN payroll_entries.remainder_amount IS
    'TT §2.3.2 snapshot: remainder portion (base_salary − advance_amount).';
COMMENT ON COLUMN payroll_entries.advance_paid_day IS
    'TT §2.4: day of month (1..31) the advance was actually paid. NULL = not yet paid.';
COMMENT ON COLUMN payroll_entries.remainder_paid_day IS
    'TT §2.4: day of month (1..31) the remainder was actually paid. NULL = not yet paid.';
COMMENT ON COLUMN payroll_entries.position_snapshot IS
    'TT §2.1 immutability: job title captured at payroll-entry creation time.';
COMMENT ON TABLE payroll_settings IS
    'TT §2.2: tenant-wide payroll configuration (advance percent, currency, company name for print header).';
