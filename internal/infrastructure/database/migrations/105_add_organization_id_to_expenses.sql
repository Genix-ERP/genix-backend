-- Migration 103: Add organization_id to expenses

ALTER TABLE expenses ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
CREATE INDEX IF NOT EXISTS idx_expenses_organization ON expenses(organization_id);

-- Backfill from employee's organization (via contacts/employees)
UPDATE expenses e SET organization_id = (
    SELECT emp.organization_id FROM employees emp WHERE emp.id = e.employee_id
) WHERE e.organization_id IS NULL AND e.employee_id IS NOT NULL;

-- Fallback: backfill from vendor's organization
UPDATE expenses e SET organization_id = (
    SELECT c.organization_id FROM contacts c WHERE c.id = e.vendor_id
) WHERE e.organization_id IS NULL AND e.vendor_id IS NOT NULL;

-- Fallback to tenant's first organization
UPDATE expenses e SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = e.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE e.organization_id IS NULL;
