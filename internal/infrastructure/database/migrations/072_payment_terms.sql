-- Payment Terms Module Migration
-- Configurable payment terms for sales orders, sales_invoices, and contacts

-- Payment Terms table
CREATE TABLE IF NOT EXISTS payment_terms (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Basic info
    code VARCHAR(30) NOT NULL,
    name VARCHAR(100) NOT NULL,
    description TEXT,

    -- Term type: fixed (due in X days), end_of_month, end_of_next_month, immediate
    term_type VARCHAR(30) NOT NULL DEFAULT 'fixed',

    -- Days until due (for fixed type)
    due_days INTEGER NOT NULL DEFAULT 30,

    -- Early payment discount
    has_early_discount BOOLEAN DEFAULT false,
    discount_percentage NUMERIC(5,2) DEFAULT 0,  -- e.g., 2.00 for 2%
    discount_days INTEGER DEFAULT 0,              -- Days within which discount applies

    -- End of month settings
    end_of_month_day INTEGER,  -- If term_type is end_of_month, which day (e.g., 15 means 15th of month)

    -- Display
    display_order INTEGER DEFAULT 100,
    is_default BOOLEAN DEFAULT false,
    is_active BOOLEAN DEFAULT true,

    -- Audit
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(tenant_id, code)
);

-- Add payment_term_id to sales_orders (reference to payment_terms table)
ALTER TABLE sales_orders ADD COLUMN IF NOT EXISTS payment_term_id UUID REFERENCES payment_terms(id);

-- Add payment_term_id to sales_invoices
ALTER TABLE sales_invoices ADD COLUMN IF NOT EXISTS payment_term_id UUID REFERENCES payment_terms(id);

-- Add payment_term_id to contacts (default payment term for customer/vendor)
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS payment_term_id UUID REFERENCES payment_terms(id);

-- Add payment_term_id to purchase_orders
ALTER TABLE purchase_orders ADD COLUMN IF NOT EXISTS payment_term_id UUID REFERENCES payment_terms(id);

-- Add payment_term_id to purchase_invoices
ALTER TABLE purchase_invoices ADD COLUMN IF NOT EXISTS payment_term_id UUID REFERENCES payment_terms(id);

-- Add due_date to sales_invoices if not exists
ALTER TABLE sales_invoices ADD COLUMN IF NOT EXISTS due_date DATE;

-- Add early_discount_amount to sales_invoices
ALTER TABLE sales_invoices ADD COLUMN IF NOT EXISTS early_discount_amount NUMERIC(15,2) DEFAULT 0;
ALTER TABLE sales_invoices ADD COLUMN IF NOT EXISTS early_discount_date DATE;

-- Indexes
CREATE INDEX IF NOT EXISTS idx_payment_terms_tenant ON payment_terms(tenant_id);
CREATE INDEX IF NOT EXISTS idx_payment_terms_code ON payment_terms(tenant_id, code);
CREATE INDEX IF NOT EXISTS idx_payment_terms_default ON payment_terms(tenant_id, is_default) WHERE is_default = true;
CREATE INDEX IF NOT EXISTS idx_payment_terms_active ON payment_terms(tenant_id, is_active) WHERE is_active = true;

CREATE INDEX IF NOT EXISTS idx_sales_orders_payment_term ON sales_orders(payment_term_id) WHERE payment_term_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_sales_invoices_payment_term ON sales_invoices(payment_term_id) WHERE payment_term_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_sales_invoices_due_date ON sales_invoices(due_date) WHERE due_date IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_purchase_invoices_payment_term ON purchase_invoices(payment_term_id) WHERE payment_term_id IS NOT NULL;

-- Add payment terms permissions
INSERT INTO permissions (id, module, resource, action, description, code, name)
VALUES
    (uuid_generate_v4(), 'sales', 'payment_term', 'read', 'View payment terms', 'sales.payment_term.read', 'View Payment Terms'),
    (uuid_generate_v4(), 'sales', 'payment_term', 'create', 'Create payment terms', 'sales.payment_term.create', 'Create Payment Terms'),
    (uuid_generate_v4(), 'sales', 'payment_term', 'update', 'Update payment terms', 'sales.payment_term.update', 'Update Payment Terms'),
    (uuid_generate_v4(), 'sales', 'payment_term', 'delete', 'Delete payment terms', 'sales.payment_term.delete', 'Delete Payment Terms')
ON CONFLICT (module, resource, action) DO NOTHING;

-- Insert default payment terms
INSERT INTO payment_terms (tenant_id, code, name, description, term_type, due_days, display_order, is_default)
SELECT
    t.id,
    'IMMEDIATE',
    'Due Immediately',
    'Payment is due upon receipt',
    'immediate',
    0,
    10,
    false
FROM tenants t
ON CONFLICT (tenant_id, code) DO NOTHING;

INSERT INTO payment_terms (tenant_id, code, name, description, term_type, due_days, display_order, is_default)
SELECT
    t.id,
    'NET15',
    'Net 15',
    'Payment is due within 15 days',
    'fixed',
    15,
    20,
    false
FROM tenants t
ON CONFLICT (tenant_id, code) DO NOTHING;

INSERT INTO payment_terms (tenant_id, code, name, description, term_type, due_days, display_order, is_default)
SELECT
    t.id,
    'NET30',
    'Net 30',
    'Payment is due within 30 days',
    'fixed',
    30,
    30,
    true
FROM tenants t
ON CONFLICT (tenant_id, code) DO NOTHING;

INSERT INTO payment_terms (tenant_id, code, name, description, term_type, due_days, display_order, is_default)
SELECT
    t.id,
    'NET45',
    'Net 45',
    'Payment is due within 45 days',
    'fixed',
    45,
    40,
    false
FROM tenants t
ON CONFLICT (tenant_id, code) DO NOTHING;

INSERT INTO payment_terms (tenant_id, code, name, description, term_type, due_days, display_order, is_default)
SELECT
    t.id,
    'NET60',
    'Net 60',
    'Payment is due within 60 days',
    'fixed',
    60,
    50,
    false
FROM tenants t
ON CONFLICT (tenant_id, code) DO NOTHING;

INSERT INTO payment_terms (tenant_id, code, name, description, term_type, due_days, display_order, is_default)
SELECT
    t.id,
    'NET90',
    'Net 90',
    'Payment is due within 90 days',
    'fixed',
    90,
    60,
    false
FROM tenants t
ON CONFLICT (tenant_id, code) DO NOTHING;

INSERT INTO payment_terms (tenant_id, code, name, description, term_type, due_days, has_early_discount, discount_percentage, discount_days, display_order, is_default)
SELECT
    t.id,
    '2_10_NET30',
    '2/10 Net 30',
    '2% discount if paid within 10 days, otherwise due in 30 days',
    'fixed',
    30,
    true,
    2.00,
    10,
    35,
    false
FROM tenants t
ON CONFLICT (tenant_id, code) DO NOTHING;

INSERT INTO payment_terms (tenant_id, code, name, description, term_type, due_days, display_order, is_default)
SELECT
    t.id,
    'EOM',
    'End of Month',
    'Payment is due at the end of the current month',
    'end_of_month',
    0,
    70,
    false
FROM tenants t
ON CONFLICT (tenant_id, code) DO NOTHING;

INSERT INTO payment_terms (tenant_id, code, name, description, term_type, due_days, display_order, is_default)
SELECT
    t.id,
    'EOM15',
    'End of Month + 15',
    'Payment is due 15 days after the end of the current month',
    'end_of_month',
    15,
    80,
    false
FROM tenants t
ON CONFLICT (tenant_id, code) DO NOTHING;
