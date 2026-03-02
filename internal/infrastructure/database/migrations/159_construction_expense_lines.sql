-- =====================================================
-- Migration 159: Construction Expense Lines
-- Central entity for actual cost capture (Xarajat operatsiyasi)
-- =====================================================

CREATE TABLE IF NOT EXISTS construction_expense_lines (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    organization_id UUID REFERENCES organizations(id),
    project_id BIGINT NOT NULL REFERENCES construction_projects(id),
    stage_id BIGINT REFERENCES construction_stages(id),
    wbs_id BIGINT REFERENCES construction_wbs(id),
    cost_category_id BIGINT REFERENCES construction_cost_categories(id),

    expense_date DATE NOT NULL,
    description TEXT NOT NULL,

    -- Material fields (optional, for materials category)
    product_id UUID REFERENCES products(id),
    quantity DECIMAL(15,4),
    uom VARCHAR(50),
    unit_price DECIMAL(15,2),

    -- Financial
    amount DECIMAL(15,2) NOT NULL DEFAULT 0,
    currency_code VARCHAR(10) DEFAULT 'UZS',

    -- Counterparty
    vendor_id UUID REFERENCES contacts(id),

    -- Accounting
    debit_account_id UUID REFERENCES accounts(id),    -- defaults to WIP (0810 mapping)
    credit_account_id UUID REFERENCES accounts(id),   -- supplier/payroll/cash/bank
    analytic_account_id UUID REFERENCES accounts(id), -- project analytic account
    journal_entry_id UUID REFERENCES journal_entries(id),

    -- Document attachment
    document_url TEXT,

    -- Workflow
    status VARCHAR(50) DEFAULT 'draft',  -- draft, approved, cancelled
    approved_by UUID REFERENCES employees(id),
    approved_at TIMESTAMP,
    cancelled_reason TEXT,
    cancelled_at TIMESTAMP,

    -- Audit
    created_by UUID,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    deleted_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_cel_project ON construction_expense_lines(tenant_id, project_id);
CREATE INDEX IF NOT EXISTS idx_cel_stage ON construction_expense_lines(stage_id);
CREATE INDEX IF NOT EXISTS idx_cel_status ON construction_expense_lines(status);
CREATE INDEX IF NOT EXISTS idx_cel_date ON construction_expense_lines(expense_date);
