-- Sales Quotations table for sales quotes/proposals (separate from CRM quotations)
CREATE TABLE IF NOT EXISTS sales_quotations (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    quotation_number VARCHAR(50) NOT NULL,
    customer_id UUID REFERENCES contacts(id) ON DELETE SET NULL,
    customer_name VARCHAR(255),
    contact_person VARCHAR(255),
    email VARCHAR(255),
    valid_until DATE,

    -- Financial
    subtotal DECIMAL(20, 4) DEFAULT 0,
    discount_percent DECIMAL(10, 4) DEFAULT 0,
    discount_amount DECIMAL(20, 4) DEFAULT 0,
    tax_percent DECIMAL(10, 4) DEFAULT 12,
    tax_amount DECIMAL(20, 4) DEFAULT 0,
    total_amount DECIMAL(20, 4) DEFAULT 0,

    -- Status: draft, sent, accepted, rejected, expired
    status VARCHAR(30) DEFAULT 'draft',

    -- Conversion tracking
    converted_to_order VARCHAR(50),
    sales_order_id UUID REFERENCES sales_orders(id) ON DELETE SET NULL,

    notes TEXT,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,

    UNIQUE(tenant_id, quotation_number)
);

-- Sales Quotation line items
CREATE TABLE IF NOT EXISTS sales_quotation_items (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    quotation_id UUID NOT NULL REFERENCES sales_quotations(id) ON DELETE CASCADE,
    product_id UUID REFERENCES products(id) ON DELETE SET NULL,
    product_name VARCHAR(255) NOT NULL,
    quantity DECIMAL(20, 4) NOT NULL DEFAULT 1,
    unit_price DECIMAL(20, 4) NOT NULL DEFAULT 0,
    total DECIMAL(20, 4) NOT NULL DEFAULT 0,
    notes TEXT,
    sort_order INTEGER DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_sales_quotations_tenant ON sales_quotations(tenant_id);
CREATE INDEX IF NOT EXISTS idx_sales_quotations_customer ON sales_quotations(customer_id);
CREATE INDEX IF NOT EXISTS idx_sales_quotations_status ON sales_quotations(status);
CREATE INDEX IF NOT EXISTS idx_sales_quotations_number ON sales_quotations(quotation_number);
CREATE INDEX IF NOT EXISTS idx_sales_quotation_items_quotation ON sales_quotation_items(quotation_id);
