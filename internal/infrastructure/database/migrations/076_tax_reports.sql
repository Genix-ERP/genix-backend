-- Tax Reports Module Migration
-- Tax reporting, VAT summaries, and tax liability tracking

-- Tax Report Periods (monthly/quarterly tax filing periods)
CREATE TABLE IF NOT EXISTS tax_report_periods (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Period info
    name VARCHAR(255) NOT NULL,
    period_type VARCHAR(20) NOT NULL DEFAULT 'monthly', -- monthly, quarterly, yearly
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,

    -- Status
    status VARCHAR(30) DEFAULT 'draft', -- draft, calculated, submitted, filed

    -- Calculated totals
    total_sales DECIMAL(15,2) DEFAULT 0,
    total_sales_tax DECIMAL(15,2) DEFAULT 0, -- Output VAT (tax collected)
    total_purchases DECIMAL(15,2) DEFAULT 0,
    total_purchase_tax DECIMAL(15,2) DEFAULT 0, -- Input VAT (tax paid)
    net_tax_liability DECIMAL(15,2) DEFAULT 0, -- Output - Input (amount owed/refundable)

    -- Filing info
    filing_reference VARCHAR(100),
    filed_date DATE,
    filed_by UUID REFERENCES users(id),

    -- Notes
    notes TEXT,

    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(tenant_id, period_type, start_date)
);

-- Tax Report Lines (detailed breakdown by tax rate)
CREATE TABLE IF NOT EXISTS tax_report_lines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    report_id UUID NOT NULL REFERENCES tax_report_periods(id) ON DELETE CASCADE,

    -- Tax info
    tax_rate_id UUID REFERENCES tax_rates(id),
    tax_name VARCHAR(255),
    tax_rate DECIMAL(5,2),

    -- Transaction type
    transaction_type VARCHAR(30) NOT NULL, -- sales, purchases

    -- Totals for this tax rate
    taxable_amount DECIMAL(15,2) DEFAULT 0, -- Base amount before tax
    tax_amount DECIMAL(15,2) DEFAULT 0, -- Tax collected/paid
    total_amount DECIMAL(15,2) DEFAULT 0, -- Total including tax

    -- Transaction counts
    transaction_count INTEGER DEFAULT 0,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Tax Transactions Log (individual transactions for audit)
CREATE TABLE IF NOT EXISTS tax_transactions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    report_id UUID REFERENCES tax_report_periods(id) ON DELETE SET NULL,

    -- Transaction reference
    transaction_type VARCHAR(30) NOT NULL, -- sales_invoice, purchase_invoice, credit_note, expense
    transaction_id UUID NOT NULL,
    transaction_number VARCHAR(50),
    transaction_date DATE NOT NULL,

    -- Party info
    party_type VARCHAR(20), -- customer, vendor
    party_id UUID,
    party_name VARCHAR(255),
    party_tax_id VARCHAR(50), -- Customer/Vendor TIN

    -- Tax details
    tax_rate_id UUID REFERENCES tax_rates(id),
    tax_name VARCHAR(255),
    tax_rate DECIMAL(5,2),

    -- Amounts
    taxable_amount DECIMAL(15,2) NOT NULL DEFAULT 0,
    tax_amount DECIMAL(15,2) NOT NULL DEFAULT 0,
    total_amount DECIMAL(15,2) NOT NULL DEFAULT 0,

    -- For corrections/adjustments
    is_correction BOOLEAN DEFAULT false,
    original_transaction_id UUID,
    correction_reason TEXT,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Tax Report Settings (per tenant configuration)
CREATE TABLE IF NOT EXISTS tax_report_settings (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Default period type
    default_period_type VARCHAR(20) DEFAULT 'monthly',

    -- Tax registration info
    tax_registration_number VARCHAR(100),
    tax_office VARCHAR(255),

    -- Auto-calculation settings
    auto_include_sales_invoices BOOLEAN DEFAULT true,
    auto_include_purchase_invoices BOOLEAN DEFAULT true,
    auto_include_expenses BOOLEAN DEFAULT true,
    auto_include_credit_notes BOOLEAN DEFAULT true,

    -- Rounding
    rounding_method VARCHAR(20) DEFAULT 'round', -- round, floor, ceiling

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(tenant_id)
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_tax_report_periods_tenant ON tax_report_periods(tenant_id);
CREATE INDEX IF NOT EXISTS idx_tax_report_periods_dates ON tax_report_periods(start_date, end_date);
CREATE INDEX IF NOT EXISTS idx_tax_report_periods_status ON tax_report_periods(status);
CREATE INDEX IF NOT EXISTS idx_tax_report_periods_type ON tax_report_periods(period_type);

CREATE INDEX IF NOT EXISTS idx_tax_report_lines_report ON tax_report_lines(report_id);
CREATE INDEX IF NOT EXISTS idx_tax_report_lines_tax ON tax_report_lines(tax_rate_id);

CREATE INDEX IF NOT EXISTS idx_tax_transactions_tenant ON tax_transactions(tenant_id);
CREATE INDEX IF NOT EXISTS idx_tax_transactions_report ON tax_transactions(report_id);
CREATE INDEX IF NOT EXISTS idx_tax_transactions_date ON tax_transactions(transaction_date);
CREATE INDEX IF NOT EXISTS idx_tax_transactions_type ON tax_transactions(transaction_type);
CREATE INDEX IF NOT EXISTS idx_tax_transactions_ref ON tax_transactions(transaction_type, transaction_id);

-- Add tax report permissions
INSERT INTO permissions (id, module, resource, action, description)
VALUES
    (uuid_generate_v4(), 'finance', 'tax_report', 'read', 'View tax reports'),
    (uuid_generate_v4(), 'finance', 'tax_report', 'create', 'Create tax reports'),
    (uuid_generate_v4(), 'finance', 'tax_report', 'update', 'Update tax reports'),
    (uuid_generate_v4(), 'finance', 'tax_report', 'delete', 'Delete tax reports'),
    (uuid_generate_v4(), 'finance', 'tax_report', 'file', 'File tax reports')
ON CONFLICT (module, resource, action) DO NOTHING;

-- Initialize tax report settings for existing tenants
INSERT INTO tax_report_settings (tenant_id)
SELECT id FROM tenants t
WHERE NOT EXISTS (SELECT 1 FROM tax_report_settings WHERE tenant_id = t.id);
