-- Discounts and Promotions table
CREATE TABLE IF NOT EXISTS discounts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Basic info
    code VARCHAR(50) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,

    -- Discount type and value
    discount_type VARCHAR(20) NOT NULL DEFAULT 'percentage', -- percentage, fixed_amount
    discount_value DECIMAL(20, 4) NOT NULL DEFAULT 0,

    -- Conditions
    min_order_amount DECIMAL(20, 4) DEFAULT 0,
    max_discount_amount DECIMAL(20, 4), -- cap for percentage discounts
    usage_limit INTEGER, -- null means unlimited
    usage_per_customer INTEGER DEFAULT 1,
    used_count INTEGER DEFAULT 0,

    -- Applicability
    applies_to VARCHAR(20) DEFAULT 'all', -- all, specific_products, specific_categories, specific_customers
    applicable_products UUID[], -- array of product IDs
    applicable_categories UUID[], -- array of category IDs
    applicable_customers UUID[], -- array of customer IDs
    new_customers_only BOOLEAN DEFAULT FALSE,

    -- Validity period
    valid_from DATE NOT NULL,
    valid_until DATE NOT NULL,

    -- Status
    status VARCHAR(20) DEFAULT 'active', -- active, inactive, expired

    -- Audit
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,

    UNIQUE(tenant_id, code)
);

-- Discount usage tracking
CREATE TABLE IF NOT EXISTS discount_usage (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    discount_id UUID NOT NULL REFERENCES discounts(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    customer_id UUID REFERENCES contacts(id),
    sales_order_id UUID REFERENCES sales_orders(id),
    amount_discounted DECIMAL(20, 4) NOT NULL,
    used_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_discounts_tenant ON discounts(tenant_id);
CREATE INDEX IF NOT EXISTS idx_discounts_code ON discounts(tenant_id, code);
CREATE INDEX IF NOT EXISTS idx_discounts_status ON discounts(tenant_id, status);
CREATE INDEX IF NOT EXISTS idx_discounts_valid ON discounts(tenant_id, valid_from, valid_until);
CREATE INDEX IF NOT EXISTS idx_discount_usage_discount ON discount_usage(discount_id);
CREATE INDEX IF NOT EXISTS idx_discount_usage_customer ON discount_usage(customer_id);

-- Add permissions for discounts
INSERT INTO permissions (id, module, resource, action, description)
VALUES
    (uuid_generate_v4(), 'sales', 'discount', 'read', 'View discounts'),
    (uuid_generate_v4(), 'sales', 'discount', 'create', 'Create discounts'),
    (uuid_generate_v4(), 'sales', 'discount', 'update', 'Update discounts'),
    (uuid_generate_v4(), 'sales', 'discount', 'delete', 'Delete discounts')
ON CONFLICT DO NOTHING;
