-- Inventory shortage deductions: bridge between inventory and payroll
-- Adds employee_deductions table and extends stock_count_lines with resolution/responsible fields

-- 1. Add resolution and responsible_emp_id to stock_count_lines
ALTER TABLE stock_count_lines ADD COLUMN IF NOT EXISTS resolution VARCHAR(20) DEFAULT 'pending';
ALTER TABLE stock_count_lines ADD COLUMN IF NOT EXISTS responsible_emp_id UUID REFERENCES employees(id);

CREATE INDEX IF NOT EXISTS idx_scl_resolution ON stock_count_lines(resolution);
CREATE INDEX IF NOT EXISTS idx_scl_responsible ON stock_count_lines(responsible_emp_id);

-- 2. Create employee_deductions table
CREATE TABLE IF NOT EXISTS employee_deductions (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        UUID NOT NULL REFERENCES tenants(id),
    organization_id  UUID REFERENCES organizations(id),
    employee_id      UUID NOT NULL REFERENCES employees(id),
    amount           DECIMAL(15,2) NOT NULL,
    reason           TEXT NOT NULL,
    source_type      VARCHAR(50) NOT NULL,
        -- 'inventory_shortage' | 'penalty' | 'advance' | 'other'
    source_id        UUID,  -- stock_count_lines.id or other source
    status           VARCHAR(20) NOT NULL DEFAULT 'pending',
        -- 'pending'  : not yet deducted
        -- 'deducted' : deducted from salary
        -- 'paid_cash': paid in cash
        -- 'cancelled': cancelled
    payroll_entry_id UUID REFERENCES payroll_entries(id),
    deducted_at      TIMESTAMPTZ,
    cancelled_reason TEXT,
    cancelled_by     UUID REFERENCES users(id),
    cancelled_at     TIMESTAMPTZ,
    created_by       UUID NOT NULL REFERENCES users(id),
    created_at       TIMESTAMPTZ DEFAULT NOW(),
    updated_at       TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_ed_tenant ON employee_deductions(tenant_id);
CREATE INDEX IF NOT EXISTS idx_ed_employee ON employee_deductions(employee_id);
CREATE INDEX IF NOT EXISTS idx_ed_status ON employee_deductions(status);
CREATE INDEX IF NOT EXISTS idx_ed_source ON employee_deductions(source_type, source_id);
