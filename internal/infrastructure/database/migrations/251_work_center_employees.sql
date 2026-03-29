-- Work Center Employees junction table (many-to-many)
CREATE TABLE IF NOT EXISTS work_center_employees (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    work_center_id UUID NOT NULL REFERENCES work_centers(id) ON DELETE CASCADE,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    role VARCHAR(50) DEFAULT 'operator',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(tenant_id, work_center_id, employee_id)
);

CREATE INDEX IF NOT EXISTS idx_wce_tenant ON work_center_employees(tenant_id);
CREATE INDEX IF NOT EXISTS idx_wce_work_center ON work_center_employees(work_center_id);
CREATE INDEX IF NOT EXISTS idx_wce_employee ON work_center_employees(employee_id);
