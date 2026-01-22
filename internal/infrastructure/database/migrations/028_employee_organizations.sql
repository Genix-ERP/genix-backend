-- Junction table for assigning employees to multiple organizations (companies)
-- An employee can work for multiple companies within the same tenant

CREATE TABLE IF NOT EXISTS employee_organizations (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    role VARCHAR(100),  -- Optional: employee's role in this organization
    is_primary BOOLEAN DEFAULT false,  -- Is this the employee's primary organization?
    start_date DATE,
    end_date DATE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    -- Each employee can only be assigned once to each organization
    UNIQUE(employee_id, organization_id)
);

-- Indexes for faster lookups
CREATE INDEX IF NOT EXISTS idx_employee_organizations_tenant ON employee_organizations(tenant_id);
CREATE INDEX IF NOT EXISTS idx_employee_organizations_employee ON employee_organizations(employee_id);
CREATE INDEX IF NOT EXISTS idx_employee_organizations_organization ON employee_organizations(organization_id);
CREATE INDEX IF NOT EXISTS idx_employee_organizations_primary ON employee_organizations(employee_id, is_primary) WHERE is_primary = true;

-- Add organization_id to employees table for default/primary organization
ALTER TABLE employees ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
CREATE INDEX IF NOT EXISTS idx_employees_organization ON employees(organization_id);
