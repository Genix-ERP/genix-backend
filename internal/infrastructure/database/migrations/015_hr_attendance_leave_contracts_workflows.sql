-- GenixERP HR Extended Modules & Workflow Automation Schema Migration
-- Attendance Records, Leave Management, Employee Contracts, Workflows

-- ============================================
-- ATTENDANCE RECORDS
-- ============================================
CREATE TABLE attendance_records (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    employee_name VARCHAR(255),
    department VARCHAR(100),
    date DATE NOT NULL,
    clock_in TIMESTAMP WITH TIME ZONE,
    clock_out TIMESTAMP WITH TIME ZONE,
    status VARCHAR(20) NOT NULL, -- present, absent, late, half_day, on_leave
    break_duration INTEGER DEFAULT 0, -- in minutes
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, employee_id, date)
);

CREATE INDEX idx_attendance_tenant ON attendance_records(tenant_id);
CREATE INDEX idx_attendance_employee ON attendance_records(employee_id);
CREATE INDEX idx_attendance_date ON attendance_records(tenant_id, date);
CREATE INDEX idx_attendance_status ON attendance_records(tenant_id, status);

-- ============================================
-- LEAVE REQUESTS
-- ============================================
CREATE TABLE leave_requests (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    employee_name VARCHAR(255),
    leave_type VARCHAR(50) NOT NULL, -- annual, sick, personal, unpaid, etc.
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    days DECIMAL(5, 1) NOT NULL, -- Can be 0.5 for half day
    reason TEXT NOT NULL,
    status VARCHAR(20) DEFAULT 'pending', -- pending, approved, rejected
    half_day BOOLEAN DEFAULT false,
    half_day_period VARCHAR(20), -- morning, afternoon
    approved_by UUID REFERENCES users(id),
    approved_at TIMESTAMP WITH TIME ZONE,
    rejection_note TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX idx_leave_requests_tenant ON leave_requests(tenant_id);
CREATE INDEX idx_leave_requests_employee ON leave_requests(employee_id);
CREATE INDEX idx_leave_requests_status ON leave_requests(tenant_id, status);
CREATE INDEX idx_leave_requests_dates ON leave_requests(start_date, end_date);

-- ============================================
-- LEAVE BALANCES
-- ============================================
CREATE TABLE leave_balances (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,

    -- Annual leave
    annual_total DECIMAL(10, 2) DEFAULT 0,
    annual_used DECIMAL(10, 2) DEFAULT 0,
    annual_pending DECIMAL(10, 2) DEFAULT 0,
    annual_remaining DECIMAL(10, 2) DEFAULT 0,

    -- Sick leave
    sick_total DECIMAL(10, 2) DEFAULT 0,
    sick_used DECIMAL(10, 2) DEFAULT 0,
    sick_pending DECIMAL(10, 2) DEFAULT 0,
    sick_remaining DECIMAL(10, 2) DEFAULT 0,

    -- Personal leave
    personal_total DECIMAL(10, 2) DEFAULT 0,
    personal_used DECIMAL(10, 2) DEFAULT 0,
    personal_pending DECIMAL(10, 2) DEFAULT 0,
    personal_remaining DECIMAL(10, 2) DEFAULT 0,

    -- Unpaid leave
    unpaid_used DECIMAL(10, 2) DEFAULT 0,

    year INTEGER NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, employee_id, year)
);

CREATE INDEX idx_leave_balances_tenant ON leave_balances(tenant_id);
CREATE INDEX idx_leave_balances_employee ON leave_balances(employee_id);
CREATE INDEX idx_leave_balances_year ON leave_balances(tenant_id, year);

-- ============================================
-- EMPLOYEE CONTRACTS (HR Contracts - different from procurement contracts table)
-- ============================================
CREATE TABLE employee_contracts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    employee_name VARCHAR(255),
    job_title VARCHAR(255),
    department VARCHAR(100),
    contract_type VARCHAR(50) NOT NULL, -- permanent, fixed_term, probation, contractor, part_time
    start_date DATE NOT NULL,
    end_date DATE,
    salary DECIMAL(20, 4) NOT NULL,
    currency VARCHAR(10) DEFAULT 'UZS',
    working_hours INTEGER DEFAULT 40, -- hours per week
    probation_period INTEGER DEFAULT 0, -- months
    notice_period INTEGER DEFAULT 30, -- days
    benefits TEXT, -- JSON or text description
    terms TEXT, -- Contract terms and conditions
    status VARCHAR(20) DEFAULT 'active', -- active, draft, terminated, expired
    signed_date DATE,
    termination_date DATE,
    termination_note TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX idx_employee_contracts_tenant ON employee_contracts(tenant_id);
CREATE INDEX idx_employee_contracts_employee ON employee_contracts(employee_id);
CREATE INDEX idx_employee_contracts_status ON employee_contracts(tenant_id, status);
CREATE INDEX idx_employee_contracts_dates ON employee_contracts(start_date, end_date);

-- ============================================
-- WORKFLOWS (Automation & Process Workflows)
-- ============================================
CREATE TABLE workflows (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    category VARCHAR(50) NOT NULL, -- hr, procurement, customer_support, sales, inventory, finance, marketing
    trigger VARCHAR(50) NOT NULL, -- manual, scheduled, event_based, ai_triggered
    status VARCHAR(20) DEFAULT 'draft', -- active, paused, draft
    automation_level VARCHAR(50) DEFAULT 'manual', -- manual, semi_automated, fully_automated
    steps JSONB DEFAULT '[]', -- Array of workflow steps
    success_rate DECIMAL(5, 2) DEFAULT 0, -- Percentage (0-100)
    avg_completion_time DECIMAL(10, 2) DEFAULT 0, -- Hours
    cost_savings DECIMAL(20, 4) DEFAULT 0, -- Monthly savings in currency
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX idx_workflows_tenant ON workflows(tenant_id);
CREATE INDEX idx_workflows_category ON workflows(tenant_id, category);
CREATE INDEX idx_workflows_status ON workflows(tenant_id, status);
CREATE INDEX idx_workflows_trigger ON workflows(trigger);

-- ============================================
-- COMMENTS & NOTES
-- ============================================
COMMENT ON TABLE attendance_records IS 'Employee attendance tracking with clock in/out times';
COMMENT ON TABLE leave_requests IS 'Employee leave requests with approval workflow';
COMMENT ON TABLE leave_balances IS 'Employee leave balance tracking by year and type';
COMMENT ON TABLE employee_contracts IS 'HR employee contracts (separate from procurement contracts)';
COMMENT ON TABLE workflows IS 'Business process automation workflows';

COMMENT ON COLUMN attendance_records.status IS 'Attendance status: present, absent, late, half_day, on_leave';
COMMENT ON COLUMN leave_requests.status IS 'Leave request status: pending, approved, rejected';
COMMENT ON COLUMN leave_requests.days IS 'Number of leave days (can be 0.5 for half day)';
COMMENT ON COLUMN leave_balances.annual_total IS 'Total annual leave days allocated';
COMMENT ON COLUMN leave_balances.annual_remaining IS 'Remaining annual leave days available';
COMMENT ON COLUMN employee_contracts.contract_type IS 'Contract type: permanent, fixed_term, probation, contractor, part_time';
COMMENT ON COLUMN employee_contracts.working_hours IS 'Working hours per week';
COMMENT ON COLUMN employee_contracts.probation_period IS 'Probation period in months';
COMMENT ON COLUMN employee_contracts.notice_period IS 'Notice period in days';
COMMENT ON COLUMN workflows.steps IS 'JSON array of workflow steps with step_name, action, assignee, estimated_duration';
COMMENT ON COLUMN workflows.success_rate IS 'Workflow success rate percentage (0-100)';
COMMENT ON COLUMN workflows.avg_completion_time IS 'Average completion time in hours';
COMMENT ON COLUMN workflows.cost_savings IS 'Monthly cost savings from automation';
