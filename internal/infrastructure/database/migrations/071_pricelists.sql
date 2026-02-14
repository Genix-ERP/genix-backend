-- Pricelists Module Migration
-- Enables dynamic pricing with customer-specific prices, quantity breaks, and formulas

-- Main pricelists table
CREATE TABLE IF NOT EXISTS pricelists (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Basic info
    name VARCHAR(255) NOT NULL,
    code VARCHAR(50),
    description TEXT,

    -- Currency (optional, defaults to company currency)
    currency_id UUID REFERENCES currencies(id),

    -- Type: sale (for selling), purchase (for buying)
    pricelist_type VARCHAR(20) NOT NULL DEFAULT 'sale',

    -- Validity period
    start_date DATE,
    end_date DATE,

    -- Priority (lower = higher priority when multiple pricelists match)
    priority INTEGER DEFAULT 100,

    -- Discount visibility
    show_discount BOOLEAN DEFAULT true,

    -- Country/region restrictions (JSON array of country codes)
    country_codes JSONB DEFAULT '[]',

    -- Status
    is_active BOOLEAN DEFAULT true,
    is_default BOOLEAN DEFAULT false,

    -- Metadata
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(tenant_id, code)
);

-- Pricelist items/rules table
CREATE TABLE IF NOT EXISTS pricelist_items (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    pricelist_id UUID NOT NULL REFERENCES pricelists(id) ON DELETE CASCADE,

    -- Apply to: all_products, product_category, product, product_variant
    applied_on VARCHAR(30) NOT NULL DEFAULT 'all_products',

    -- Reference to product/category (based on applied_on)
    product_id UUID REFERENCES products(id) ON DELETE CASCADE,
    category_id UUID REFERENCES product_categories(id) ON DELETE CASCADE,
    variant_id UUID REFERENCES product_variants(id) ON DELETE CASCADE,

    -- Minimum quantity for this rule to apply
    min_quantity DECIMAL(15,4) DEFAULT 1,

    -- Computation method: fixed_price, percentage, formula
    compute_price VARCHAR(20) NOT NULL DEFAULT 'fixed_price',

    -- For fixed_price: the actual price
    fixed_price DECIMAL(15,2),

    -- For percentage: discount percentage (positive = discount, negative = markup)
    percent_price DECIMAL(10,2) DEFAULT 0,

    -- For formula computation
    -- base: list_price, cost_price, other_pricelist
    price_base VARCHAR(30) DEFAULT 'list_price',
    base_pricelist_id UUID REFERENCES pricelists(id),

    -- Formula adjustments
    price_discount DECIMAL(10,2) DEFAULT 0,        -- Percentage discount
    price_surcharge DECIMAL(15,2) DEFAULT 0,       -- Fixed amount to add/subtract
    price_round DECIMAL(15,4) DEFAULT 0.01,        -- Round to nearest (e.g., 0.01, 1, 10)
    price_min_margin DECIMAL(15,2) DEFAULT 0,      -- Minimum margin (% above cost)
    price_max_margin DECIMAL(15,2) DEFAULT 0,      -- Maximum margin (% above cost)

    -- Validity period for this specific rule
    date_start DATE,
    date_end DATE,

    -- Sequence for ordering rules (lower = higher priority)
    sequence INTEGER DEFAULT 10,

    -- Status
    is_active BOOLEAN DEFAULT true,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Customer pricelist assignments
-- Links customers to specific pricelists
CREATE TABLE IF NOT EXISTS customer_pricelists (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    customer_id UUID NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    pricelist_id UUID NOT NULL REFERENCES pricelists(id) ON DELETE CASCADE,

    -- Priority when customer has multiple pricelists
    priority INTEGER DEFAULT 100,

    -- Validity
    start_date DATE,
    end_date DATE,

    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(tenant_id, customer_id, pricelist_id)
);

-- Add pricelist reference to contacts (default pricelist for customer)
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS pricelist_id UUID REFERENCES pricelists(id);

-- Add pricelist reference to sales orders
ALTER TABLE sales_orders ADD COLUMN IF NOT EXISTS pricelist_id UUID REFERENCES pricelists(id);

-- Add pricelist reference to quotations
ALTER TABLE quotations ADD COLUMN IF NOT EXISTS pricelist_id UUID REFERENCES pricelists(id);

-- Indexes for performance
CREATE INDEX IF NOT EXISTS idx_pricelists_tenant ON pricelists(tenant_id);
CREATE INDEX IF NOT EXISTS idx_pricelists_type ON pricelists(pricelist_type);
CREATE INDEX IF NOT EXISTS idx_pricelists_active ON pricelists(is_active);
CREATE INDEX IF NOT EXISTS idx_pricelists_default ON pricelists(is_default) WHERE is_default = true;
CREATE INDEX IF NOT EXISTS idx_pricelists_dates ON pricelists(start_date, end_date);

CREATE INDEX IF NOT EXISTS idx_pricelist_items_pricelist ON pricelist_items(pricelist_id);
CREATE INDEX IF NOT EXISTS idx_pricelist_items_product ON pricelist_items(product_id);
CREATE INDEX IF NOT EXISTS idx_pricelist_items_category ON pricelist_items(category_id);
CREATE INDEX IF NOT EXISTS idx_pricelist_items_variant ON pricelist_items(variant_id);
CREATE INDEX IF NOT EXISTS idx_pricelist_items_applied ON pricelist_items(applied_on);
CREATE INDEX IF NOT EXISTS idx_pricelist_items_dates ON pricelist_items(date_start, date_end);
CREATE INDEX IF NOT EXISTS idx_pricelist_items_sequence ON pricelist_items(sequence);

CREATE INDEX IF NOT EXISTS idx_customer_pricelists_tenant ON customer_pricelists(tenant_id);
CREATE INDEX IF NOT EXISTS idx_customer_pricelists_customer ON customer_pricelists(customer_id);
CREATE INDEX IF NOT EXISTS idx_customer_pricelists_pricelist ON customer_pricelists(pricelist_id);

CREATE INDEX IF NOT EXISTS idx_contacts_pricelist ON contacts(pricelist_id) WHERE pricelist_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_sales_orders_pricelist ON sales_orders(pricelist_id) WHERE pricelist_id IS NOT NULL;

-- Add pricelist permissions
INSERT INTO permissions (id, module, resource, action, description)
VALUES
    (uuid_generate_v4(), 'sales', 'pricelist', 'read', 'View pricelists'),
    (uuid_generate_v4(), 'sales', 'pricelist', 'create', 'Create pricelists'),
    (uuid_generate_v4(), 'sales', 'pricelist', 'update', 'Update pricelists'),
    (uuid_generate_v4(), 'sales', 'pricelist', 'delete', 'Delete pricelists')
ON CONFLICT (module, resource, action) DO NOTHING;

-- Insert default pricelist for existing tenants
INSERT INTO pricelists (tenant_id, name, code, description, pricelist_type, is_active, is_default)
SELECT
    id,
    'Standard Pricelist',
    'STANDARD',
    'Default pricelist using product list prices',
    'sale',
    true,
    true
FROM tenants
WHERE NOT EXISTS (
    SELECT 1 FROM pricelists WHERE pricelists.tenant_id = tenants.id AND is_default = true
);
