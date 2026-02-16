-- GenixERP Extended Modules Schema Migration
-- Contracts, Payroll, Expenses, Fixed Assets, Projects

-- ============================================
-- CONTRACTS (Supplier/Vendor Contracts)
-- ============================================
CREATE TABLE contracts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    contract_number VARCHAR(50) NOT NULL,
    supplier_id UUID REFERENCES contacts(id) ON DELETE SET NULL,
    supplier_name VARCHAR(255) NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    contract_type VARCHAR(50) NOT NULL, -- annual, fixed, project, framework
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    value DECIMAL(20, 4) DEFAULT 0,
    currency VARCHAR(10) DEFAULT 'UZS',
    payment_terms VARCHAR(50),
    terms TEXT,
    auto_renew BOOLEAN DEFAULT false,
    renewal_notice_days INTEGER DEFAULT 30,
    status VARCHAR(20) DEFAULT 'draft', -- draft, active, expired, terminated, renewed
    attachments JSONB DEFAULT '[]',
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, contract_number)
);

CREATE INDEX idx_contracts_tenant ON contracts(tenant_id);
CREATE INDEX idx_contracts_supplier ON contracts(supplier_id);
CREATE INDEX idx_contracts_status ON contracts(tenant_id, status);
CREATE INDEX idx_contracts_dates ON contracts(start_date, end_date);

-- ============================================
-- PAYROLL
-- ============================================
CREATE TABLE payroll_periods (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    period_code VARCHAR(50) NOT NULL,
    period_name VARCHAR(100) NOT NULL,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    pay_date DATE NOT NULL,
    status VARCHAR(20) DEFAULT 'draft', -- draft, processing, approved, paid, cancelled
    total_gross DECIMAL(20, 4) DEFAULT 0,
    total_deductions DECIMAL(20, 4) DEFAULT 0,
    total_net DECIMAL(20, 4) DEFAULT 0,
    employee_count INTEGER DEFAULT 0,
    notes TEXT,
    approved_by UUID REFERENCES users(id),
    approved_at TIMESTAMP WITH TIME ZONE,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, period_code)
);

CREATE INDEX idx_payroll_periods_tenant ON payroll_periods(tenant_id);
CREATE INDEX idx_payroll_periods_status ON payroll_periods(tenant_id, status);
CREATE INDEX idx_payroll_periods_dates ON payroll_periods(start_date, end_date);

CREATE TABLE payroll_entries (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    payroll_period_id UUID NOT NULL REFERENCES payroll_periods(id) ON DELETE CASCADE,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    employee_name VARCHAR(255) NOT NULL,
    base_salary DECIMAL(20, 4) DEFAULT 0,
    overtime_hours DECIMAL(10, 2) DEFAULT 0,
    overtime_amount DECIMAL(20, 4) DEFAULT 0,
    bonus DECIMAL(20, 4) DEFAULT 0,
    allowances DECIMAL(20, 4) DEFAULT 0,
    gross_salary DECIMAL(20, 4) DEFAULT 0,
    income_tax DECIMAL(20, 4) DEFAULT 0,
    social_security DECIMAL(20, 4) DEFAULT 0,
    pension DECIMAL(20, 4) DEFAULT 0,
    other_deductions DECIMAL(20, 4) DEFAULT 0,
    total_deductions DECIMAL(20, 4) DEFAULT 0,
    net_salary DECIMAL(20, 4) DEFAULT 0,
    payment_method VARCHAR(50) DEFAULT 'bank_transfer',
    bank_account VARCHAR(100),
    status VARCHAR(20) DEFAULT 'pending', -- pending, approved, paid, cancelled
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(payroll_period_id, employee_id)
);

CREATE INDEX idx_payroll_entries_period ON payroll_entries(payroll_period_id);
CREATE INDEX idx_payroll_entries_employee ON payroll_entries(employee_id);
CREATE INDEX idx_payroll_entries_status ON payroll_entries(status);

-- ============================================
-- EXPENSES
-- ============================================
CREATE TABLE expense_categories (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    code VARCHAR(50) NOT NULL,
    name VARCHAR(100) NOT NULL,
    description TEXT,
    parent_id UUID REFERENCES expense_categories(id) ON DELETE SET NULL,
    account_id UUID REFERENCES accounts(id),
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, code)
);

CREATE INDEX idx_expense_categories_tenant ON expense_categories(tenant_id);

CREATE TABLE expenses (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    expense_number VARCHAR(50) NOT NULL,
    category_id UUID REFERENCES expense_categories(id) ON DELETE SET NULL,
    employee_id UUID REFERENCES employees(id) ON DELETE SET NULL,
    employee_name VARCHAR(255),
    vendor_id UUID REFERENCES contacts(id) ON DELETE SET NULL,
    vendor_name VARCHAR(255),
    expense_date DATE NOT NULL,
    description TEXT NOT NULL,
    amount DECIMAL(20, 4) NOT NULL,
    tax_amount DECIMAL(20, 4) DEFAULT 0,
    total_amount DECIMAL(20, 4) NOT NULL,
    currency VARCHAR(10) DEFAULT 'UZS',
    payment_method VARCHAR(50),
    reference VARCHAR(100),
    receipt_url VARCHAR(500),
    attachments JSONB DEFAULT '[]',
    status VARCHAR(20) DEFAULT 'pending', -- pending, approved, rejected, paid, reimbursed
    reimbursable BOOLEAN DEFAULT false,
    reimbursed_date DATE,
    approved_by UUID REFERENCES users(id),
    approved_at TIMESTAMP WITH TIME ZONE,
    notes TEXT,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, expense_number)
);

CREATE INDEX idx_expenses_tenant ON expenses(tenant_id);
CREATE INDEX idx_expenses_category ON expenses(category_id);
CREATE INDEX idx_expenses_employee ON expenses(employee_id);
CREATE INDEX idx_expenses_vendor ON expenses(vendor_id);
CREATE INDEX idx_expenses_date ON expenses(expense_date);
CREATE INDEX idx_expenses_status ON expenses(tenant_id, status);

-- ============================================
-- FIXED ASSETS
-- ============================================
CREATE TABLE asset_categories (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    code VARCHAR(50) NOT NULL,
    name VARCHAR(100) NOT NULL,
    description TEXT,
    depreciation_method VARCHAR(50) DEFAULT 'straight_line', -- straight_line, declining_balance, double_declining, sum_of_years
    default_useful_life_months INTEGER DEFAULT 60,
    asset_account_id UUID REFERENCES accounts(id),
    depreciation_account_id UUID REFERENCES accounts(id),
    expense_account_id UUID REFERENCES accounts(id),
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, code)
);

CREATE INDEX idx_asset_categories_tenant ON asset_categories(tenant_id);

CREATE TABLE fixed_assets (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    asset_code VARCHAR(50) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    category_id UUID REFERENCES asset_categories(id) ON DELETE SET NULL,
    category_name VARCHAR(100),
    serial_number VARCHAR(100),
    acquisition_date DATE NOT NULL,
    acquisition_cost DECIMAL(20, 4) NOT NULL,
    salvage_value DECIMAL(20, 4) DEFAULT 0,
    useful_life_months INTEGER NOT NULL,
    depreciation_method VARCHAR(50) NOT NULL,
    accumulated_depreciation DECIMAL(20, 4) DEFAULT 0,
    book_value DECIMAL(20, 4),
    location VARCHAR(255),
    custodian_id UUID REFERENCES employees(id) ON DELETE SET NULL,
    custodian_name VARCHAR(255),
    warranty_expiry DATE,
    status VARCHAR(20) DEFAULT 'active', -- active, disposed, under_maintenance, written_off
    disposal_date DATE,
    disposal_amount DECIMAL(20, 4),
    disposal_reason TEXT,
    attachments JSONB DEFAULT '[]',
    notes TEXT,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, asset_code)
);

CREATE INDEX idx_fixed_assets_tenant ON fixed_assets(tenant_id);
CREATE INDEX idx_fixed_assets_category ON fixed_assets(category_id);
CREATE INDEX idx_fixed_assets_status ON fixed_assets(tenant_id, status);
CREATE INDEX idx_fixed_assets_custodian ON fixed_assets(custodian_id);

CREATE TABLE depreciation_entries (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    asset_id UUID NOT NULL REFERENCES fixed_assets(id) ON DELETE CASCADE,
    period VARCHAR(20) NOT NULL, -- YYYY-MM format
    depreciation_date DATE NOT NULL,
    depreciation_amount DECIMAL(20, 4) NOT NULL,
    accumulated_total DECIMAL(20, 4) NOT NULL,
    book_value_after DECIMAL(20, 4) NOT NULL,
    depreciation_method VARCHAR(50),
    journal_entry_id UUID REFERENCES journal_entries(id),
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(asset_id, period)
);

CREATE INDEX idx_depreciation_entries_asset ON depreciation_entries(asset_id);
CREATE INDEX idx_depreciation_entries_period ON depreciation_entries(period);

-- ============================================
-- PROJECTS
-- ============================================
CREATE TABLE projects (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    project_code VARCHAR(50) NOT NULL,
    project_name VARCHAR(255) NOT NULL,
    description TEXT,
    client_id UUID REFERENCES contacts(id) ON DELETE SET NULL,
    client_name VARCHAR(255),
    manager_id UUID REFERENCES employees(id) ON DELETE SET NULL,
    manager_name VARCHAR(255),
    start_date DATE NOT NULL,
    end_date DATE,
    budget DECIMAL(20, 4) DEFAULT 0,
    spent DECIMAL(20, 4) DEFAULT 0,
    billing_type VARCHAR(50) DEFAULT 'fixed_price', -- fixed_price, time_material, retainer
    hourly_rate DECIMAL(20, 4) DEFAULT 0,
    currency VARCHAR(10) DEFAULT 'UZS',
    priority VARCHAR(20) DEFAULT 'medium', -- low, medium, high, urgent
    status VARCHAR(20) DEFAULT 'planning', -- planning, active, on_hold, completed, cancelled
    progress INTEGER DEFAULT 0, -- 0-100
    tags JSONB DEFAULT '[]',
    notes TEXT,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, project_code)
);

CREATE INDEX idx_projects_tenant ON projects(tenant_id);
CREATE INDEX idx_projects_client ON projects(client_id);
CREATE INDEX idx_projects_manager ON projects(manager_id);
CREATE INDEX idx_projects_status ON projects(tenant_id, status);
CREATE INDEX idx_projects_dates ON projects(start_date, end_date);

CREATE TABLE project_team_members (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    role VARCHAR(100),
    allocation_percent INTEGER DEFAULT 100,
    start_date DATE,
    end_date DATE,
    hourly_rate DECIMAL(20, 4),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(project_id, employee_id)
);

CREATE INDEX idx_project_team_project ON project_team_members(project_id);
CREATE INDEX idx_project_team_employee ON project_team_members(employee_id);

CREATE TABLE project_tasks (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    parent_id UUID REFERENCES project_tasks(id) ON DELETE SET NULL,
    task_number VARCHAR(50) NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    assignee_id UUID REFERENCES employees(id) ON DELETE SET NULL,
    assignee_name VARCHAR(255),
    priority VARCHAR(20) DEFAULT 'medium',
    status VARCHAR(20) DEFAULT 'todo', -- todo, in_progress, review, completed, cancelled
    due_date DATE,
    estimated_hours DECIMAL(10, 2) DEFAULT 0,
    actual_hours DECIMAL(10, 2) DEFAULT 0,
    tags JSONB DEFAULT '[]',
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(project_id, task_number)
);

CREATE INDEX idx_project_tasks_project ON project_tasks(project_id);
CREATE INDEX idx_project_tasks_assignee ON project_tasks(assignee_id);
CREATE INDEX idx_project_tasks_status ON project_tasks(status);

CREATE TABLE project_milestones (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    due_date DATE NOT NULL,
    status VARCHAR(20) DEFAULT 'pending', -- pending, in_progress, completed, overdue
    deliverables JSONB DEFAULT '[]',
    completed_date DATE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_project_milestones_project ON project_milestones(project_id);
CREATE INDEX idx_project_milestones_status ON project_milestones(status);

CREATE TABLE time_entries (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    task_id UUID REFERENCES project_tasks(id) ON DELETE SET NULL,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    employee_name VARCHAR(255) NOT NULL,
    entry_date DATE NOT NULL,
    hours DECIMAL(10, 2) NOT NULL,
    description TEXT,
    billable BOOLEAN DEFAULT true,
    hourly_rate DECIMAL(20, 4),
    amount DECIMAL(20, 4),
    status VARCHAR(20) DEFAULT 'draft', -- draft, submitted, approved, rejected, billed
    approved_by UUID REFERENCES users(id),
    approved_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX idx_time_entries_project ON time_entries(project_id);
CREATE INDEX idx_time_entries_task ON time_entries(task_id);
CREATE INDEX idx_time_entries_employee ON time_entries(employee_id);
CREATE INDEX idx_time_entries_date ON time_entries(entry_date);
CREATE INDEX idx_time_entries_status ON time_entries(status);

-- Note: Default expense categories and asset categories should be created
-- when a tenant is created, not in the migration, since tenant_id is required.
-- The following INSERT statements are commented out as they require existing tenants.

-- Default expense categories (create when tenant is created):
-- TRAVEL: Travel & Transportation
-- OFFICE: Office Supplies
-- IT: IT & Technology
-- UTILITIES: Utilities
-- RENT: Rent & Lease
-- MEALS: Meals & Entertainment
-- OTHER: Other Expenses

-- Default asset categories (create when tenant is created):
-- BUILDINGS: Buildings (straight_line, 480 months)
-- VEHICLES: Vehicles (straight_line, 60 months)
-- COMPUTERS: Computers & Electronics (straight_line, 36 months)
-- FURNITURE: Furniture & Fixtures (straight_line, 84 months)
-- MACHINERY: Machinery & Equipment (straight_line, 120 months)
-- INTANGIBLE: Intangible Assets (straight_line, 60 months)
