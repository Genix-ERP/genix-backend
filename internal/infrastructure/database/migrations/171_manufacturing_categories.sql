-- Manufacturing Categories
CREATE TABLE IF NOT EXISTS manufacturing_categories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    organization_id UUID REFERENCES organizations(id),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    color VARCHAR(50),
    is_active BOOLEAN DEFAULT true,
    sort_order INTEGER DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    UNIQUE(tenant_id, name)
);

ALTER TABLE production_orders
    ADD COLUMN IF NOT EXISTS manufacturing_category_id UUID REFERENCES manufacturing_categories(id);

CREATE INDEX IF NOT EXISTS idx_manufacturing_categories_tenant ON manufacturing_categories(tenant_id);
CREATE INDEX IF NOT EXISTS idx_production_orders_category ON production_orders(manufacturing_category_id);
