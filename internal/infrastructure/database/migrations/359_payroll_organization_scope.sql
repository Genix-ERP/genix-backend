-- 359_payroll_organization_scope.sql
--
-- Scope payroll periods and entries to an organization (company) within
-- a tenant. Without this, "Joriy oyga yaratish" (auto-create current
-- month payroll) would pull employees from EVERY company in the tenant
-- into a single period and miss the active-company filter the rest of
-- the app honours via X-Organization-ID. The handler at
-- buxgalteriya_payroll_tt.go:GetOrCreateCurrentMonthPayroll now reads
-- the active org and filters / stamps with these new columns.
--
-- The original (tenant_id, period_code) uniqueness is replaced with a
-- partial unique index that treats organization_id-NULL rows
-- separately from organization_id-set rows so the legacy data path
-- (single-company tenants that pre-date this change) keeps working.

-- ─── payroll_periods ─────────────────────────────────────────────────
ALTER TABLE payroll_periods
    ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_payroll_periods_organization
    ON payroll_periods (tenant_id, organization_id);

-- Drop the old constraint (tenant_id, period_code) — too coarse now
-- that we want one period per (tenant, org, month).
ALTER TABLE payroll_periods
    DROP CONSTRAINT IF EXISTS payroll_periods_tenant_id_period_code_key;

-- Replace with two partial unique indexes so legacy NULL-org rows and
-- new org-scoped rows don't collide:
--   • when organization_id IS NULL → unique by (tenant, period_code)
--   • when organization_id IS NOT NULL → unique by (tenant, org, period_code)
DROP INDEX IF EXISTS uq_payroll_periods_tenant_code_legacy;
CREATE UNIQUE INDEX uq_payroll_periods_tenant_code_legacy
    ON payroll_periods (tenant_id, period_code)
    WHERE organization_id IS NULL;

DROP INDEX IF EXISTS uq_payroll_periods_tenant_org_code;
CREATE UNIQUE INDEX uq_payroll_periods_tenant_org_code
    ON payroll_periods (tenant_id, organization_id, period_code)
    WHERE organization_id IS NOT NULL;

-- ─── payroll_entries ─────────────────────────────────────────────────
-- Stamp the same organization_id on entries so reports / exports can
-- filter without joining the period table every time. Defaults to NULL
-- for all existing rows (legacy / tenant-wide data).
ALTER TABLE payroll_entries
    ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_payroll_entries_organization
    ON payroll_entries (tenant_id, organization_id);
