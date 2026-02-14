-- Migration 104: Add organization_id to payroll tables

-- payroll_periods
ALTER TABLE payroll_periods ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
CREATE INDEX IF NOT EXISTS idx_payroll_periods_organization ON payroll_periods(organization_id);

-- Backfill to tenant's first organization
UPDATE payroll_periods pp SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = pp.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE pp.organization_id IS NULL;

-- payroll_entries
ALTER TABLE payroll_entries ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
CREATE INDEX IF NOT EXISTS idx_payroll_entries_organization ON payroll_entries(organization_id);

-- Backfill from employee's organization
UPDATE payroll_entries pe SET organization_id = (
    SELECT e.organization_id FROM employees e WHERE e.id = pe.employee_id
) WHERE pe.organization_id IS NULL AND pe.employee_id IS NOT NULL;

-- Fallback to tenant's first organization
UPDATE payroll_entries pe SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = pe.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE pe.organization_id IS NULL;
