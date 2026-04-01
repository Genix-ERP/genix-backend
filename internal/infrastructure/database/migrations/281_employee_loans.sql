-- Employee Loans module
-- Supports: loan granting by accountant, monthly auto-deduction from salary, employee self-service view

CREATE TABLE IF NOT EXISTS employee_loans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    organization_id UUID REFERENCES organizations(id),
    employee_id UUID NOT NULL REFERENCES employees(id),
    loan_number VARCHAR(50),
    amount DECIMAL(15,2) NOT NULL,              -- Total loan amount
    monthly_payment DECIMAL(15,2) NOT NULL,     -- Fixed monthly deduction
    duration_months INTEGER NOT NULL,           -- Total months
    paid_amount DECIMAL(15,2) DEFAULT 0,        -- How much has been paid back
    remaining_amount DECIMAL(15,2) NOT NULL,    -- How much is left
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    reason TEXT,                                 -- Why the loan was given
    cash_account_id UUID REFERENCES accounts(id), -- Which account money comes from (Kassa/Bank)
    status VARCHAR(20) DEFAULT 'active' CHECK (status IN ('active', 'completed', 'cancelled')),
    created_by UUID REFERENCES users(id),
    approved_by UUID REFERENCES users(id),
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS employee_loan_payments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    loan_id UUID NOT NULL REFERENCES employee_loans(id) ON DELETE CASCADE,
    employee_id UUID NOT NULL REFERENCES employees(id),
    month_label VARCHAR(50) NOT NULL,           -- e.g. "Aprel 2026"
    due_date DATE NOT NULL,                     -- When payment is due
    amount DECIMAL(15,2) NOT NULL,              -- Payment amount for this month
    remaining_after DECIMAL(15,2) NOT NULL,     -- Balance after this payment
    status VARCHAR(20) DEFAULT 'pending' CHECK (status IN ('pending', 'paid', 'overdue')),
    paid_at TIMESTAMPTZ,
    payroll_entry_id UUID REFERENCES payroll_entries(id), -- Link to payroll if deducted from salary
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_employee_loans_tenant ON employee_loans(tenant_id);
CREATE INDEX IF NOT EXISTS idx_employee_loans_employee ON employee_loans(employee_id);
CREATE INDEX IF NOT EXISTS idx_employee_loans_org ON employee_loans(organization_id);
CREATE INDEX IF NOT EXISTS idx_employee_loans_status ON employee_loans(status);
CREATE INDEX IF NOT EXISTS idx_loan_payments_loan ON employee_loan_payments(loan_id);
CREATE INDEX IF NOT EXISTS idx_loan_payments_employee ON employee_loan_payments(employee_id);
CREATE INDEX IF NOT EXISTS idx_loan_payments_status ON employee_loan_payments(status);
