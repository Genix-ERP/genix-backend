-- Per-company product settings (pricing, accounts, inventory settings)
-- Products are shared across all organizations within a tenant,
-- but each organization has its own pricing, accounts, and inventory settings.

CREATE TABLE IF NOT EXISTS product_organization_settings (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,

    -- Pricing
    cost_price DECIMAL(20, 4) DEFAULT 0,
    list_price DECIMAL(20, 4) DEFAULT 0,
    min_price DECIMAL(20, 4) DEFAULT 0,

    -- Accounting
    sales_account_id UUID REFERENCES accounts(id),
    purchase_account_id UUID REFERENCES accounts(id),
    inventory_account_id UUID REFERENCES accounts(id),
    cogs_account_id UUID REFERENCES accounts(id),

    -- Tax
    sales_tax_id UUID REFERENCES tax_rates(id),
    purchase_tax_id UUID REFERENCES tax_rates(id),

    -- Inventory settings
    min_stock_level DECIMAL(20, 4) DEFAULT 0,
    max_stock_level DECIMAL(20, 4),
    reorder_point DECIMAL(20, 4) DEFAULT 0,
    reorder_quantity DECIMAL(20, 4) DEFAULT 0,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(product_id, organization_id)
);

CREATE INDEX IF NOT EXISTS idx_pos_tenant ON product_organization_settings(tenant_id);
CREATE INDEX IF NOT EXISTS idx_pos_product ON product_organization_settings(product_id);
CREATE INDEX IF NOT EXISTS idx_pos_organization ON product_organization_settings(organization_id);

-- Migrate existing data: create a settings row for each product's current organization
INSERT INTO product_organization_settings (
    tenant_id, product_id, organization_id,
    cost_price, list_price, min_price,
    sales_account_id, purchase_account_id, inventory_account_id, cogs_account_id,
    sales_tax_id, purchase_tax_id,
    min_stock_level, max_stock_level, reorder_point, reorder_quantity
)
SELECT
    p.tenant_id, p.id, p.organization_id,
    COALESCE(p.cost_price, 0), COALESCE(p.list_price, 0), COALESCE(p.min_price, 0),
    p.sales_account_id, p.purchase_account_id, p.inventory_account_id, p.cogs_account_id,
    p.sales_tax_id, p.purchase_tax_id,
    COALESCE(p.min_stock_level, 0), p.max_stock_level, COALESCE(p.reorder_point, 0), COALESCE(p.reorder_quantity, 0)
FROM products p
WHERE p.organization_id IS NOT NULL AND p.deleted_at IS NULL
ON CONFLICT (product_id, organization_id) DO NOTHING;
