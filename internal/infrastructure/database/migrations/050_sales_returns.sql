-- Sales Returns table for product returns from customers
CREATE TABLE IF NOT EXISTS sales_returns (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    return_number VARCHAR(50) NOT NULL,

    -- Reference to invoice/order
    sales_invoice_id UUID REFERENCES sales_invoices(id) ON DELETE SET NULL,
    sales_order_id UUID REFERENCES sales_orders(id) ON DELETE SET NULL,

    -- Customer info
    customer_id UUID REFERENCES contacts(id) ON DELETE SET NULL,
    customer_name VARCHAR(255),

    -- Return details
    return_date DATE NOT NULL,
    reason VARCHAR(100), -- defective, wrong_item, damaged, not_as_described, other

    -- Financial
    subtotal DECIMAL(20, 4) DEFAULT 0,
    tax_amount DECIMAL(20, 4) DEFAULT 0,
    total_amount DECIMAL(20, 4) DEFAULT 0,

    -- Status: pending, approved, rejected, completed
    status VARCHAR(30) DEFAULT 'pending',

    -- Refund tracking
    refund_status VARCHAR(30) DEFAULT 'pending', -- pending, processed
    refund_method VARCHAR(50), -- cash, bank_transfer, credit_note, original_payment
    refund_date DATE,
    refund_reference VARCHAR(100),

    notes TEXT,
    created_by UUID REFERENCES users(id),
    approved_by UUID REFERENCES users(id),
    approved_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,

    UNIQUE(tenant_id, return_number)
);

-- Sales Return line items
CREATE TABLE IF NOT EXISTS sales_return_items (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    sales_return_id UUID NOT NULL REFERENCES sales_returns(id) ON DELETE CASCADE,
    product_id UUID REFERENCES products(id) ON DELETE SET NULL,
    product_name VARCHAR(255) NOT NULL,
    quantity DECIMAL(20, 4) NOT NULL DEFAULT 1,
    unit_price DECIMAL(20, 4) NOT NULL DEFAULT 0,
    total DECIMAL(20, 4) NOT NULL DEFAULT 0,
    condition VARCHAR(50), -- damaged, opened, sealed, used
    notes TEXT,
    sort_order INTEGER DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_sales_returns_tenant ON sales_returns(tenant_id);
CREATE INDEX IF NOT EXISTS idx_sales_returns_customer ON sales_returns(customer_id);
CREATE INDEX IF NOT EXISTS idx_sales_returns_invoice ON sales_returns(sales_invoice_id);
CREATE INDEX IF NOT EXISTS idx_sales_returns_status ON sales_returns(status);
CREATE INDEX IF NOT EXISTS idx_sales_returns_number ON sales_returns(return_number);
CREATE INDEX IF NOT EXISTS idx_sales_return_items_return ON sales_return_items(sales_return_id);
