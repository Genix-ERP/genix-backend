-- Project Expenses Table
-- Tracks expenses logged against specific projects

CREATE TABLE IF NOT EXISTS project_expenses (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    expense_number VARCHAR(50) NOT NULL,
    category VARCHAR(100),
    description TEXT NOT NULL,
    amount DECIMAL(20, 4) NOT NULL,
    currency VARCHAR(10) DEFAULT 'UZS',
    expense_date DATE NOT NULL,
    employee_id UUID REFERENCES employees(id) ON DELETE SET NULL,
    employee_name VARCHAR(255),
    vendor_name VARCHAR(255),
    receipt_url VARCHAR(500),
    billable BOOLEAN DEFAULT true,
    status VARCHAR(20) DEFAULT 'pending', -- pending, approved, rejected, reimbursed
    approved_by UUID REFERENCES users(id),
    approved_at TIMESTAMP WITH TIME ZONE,
    notes TEXT,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, expense_number)
);

CREATE INDEX IF NOT EXISTS idx_project_expenses_tenant ON project_expenses(tenant_id);
CREATE INDEX IF NOT EXISTS idx_project_expenses_project ON project_expenses(project_id);
CREATE INDEX IF NOT EXISTS idx_project_expenses_employee ON project_expenses(employee_id);
CREATE INDEX IF NOT EXISTS idx_project_expenses_date ON project_expenses(expense_date);
CREATE INDEX IF NOT EXISTS idx_project_expenses_status ON project_expenses(status);

-- Add trigger for updated_at
CREATE TRIGGER update_project_expenses_updated_at BEFORE UPDATE ON project_expenses FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Add permissions for project expenses
INSERT INTO permissions (module, resource, action, description) VALUES
('projects', 'expense', 'create', 'Create project expenses'),
('projects', 'expense', 'read', 'View project expenses'),
('projects', 'expense', 'update', 'Update project expenses'),
('projects', 'expense', 'delete', 'Delete project expenses'),
('projects', 'expense', 'approve', 'Approve project expenses')
ON CONFLICT DO NOTHING;
