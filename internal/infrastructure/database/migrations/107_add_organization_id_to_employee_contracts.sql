-- Migration 105: Add organization_id to employee_contracts

ALTER TABLE employee_contracts ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
CREATE INDEX IF NOT EXISTS idx_employee_contracts_organization ON employee_contracts(organization_id);

-- Backfill from employee's organization
UPDATE employee_contracts ec SET organization_id = (
    SELECT e.organization_id FROM employees e WHERE e.id = ec.employee_id
) WHERE ec.organization_id IS NULL AND ec.employee_id IS NOT NULL;

-- Fallback to tenant's first organization
UPDATE employee_contracts ec SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = ec.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE ec.organization_id IS NULL;
