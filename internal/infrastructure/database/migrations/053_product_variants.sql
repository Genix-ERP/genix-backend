-- Product Variants System (Odoo-style)
-- Allows products to have variants based on attributes like Size, Color, etc.

-- =====================================================
-- PRODUCT ATTRIBUTES (e.g., Size, Color, Material)
-- =====================================================
CREATE TABLE IF NOT EXISTS product_attributes (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    code VARCHAR(50),
    display_type VARCHAR(20) DEFAULT 'select', -- select, radio, color, pills
    create_variant BOOLEAN DEFAULT TRUE, -- whether this attribute creates variants
    sort_order INTEGER DEFAULT 0,
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, name)
);

-- =====================================================
-- PRODUCT ATTRIBUTE VALUES (e.g., S, M, L, XL for Size)
-- =====================================================
CREATE TABLE IF NOT EXISTS product_attribute_values (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    attribute_id UUID NOT NULL REFERENCES product_attributes(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    code VARCHAR(50),
    html_color VARCHAR(20), -- for color type attributes (e.g., #FF0000)
    sort_order INTEGER DEFAULT 0,
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(attribute_id, name)
);

-- =====================================================
-- PRODUCT TEMPLATE ATTRIBUTES (which attributes a product template uses)
-- =====================================================
CREATE TABLE IF NOT EXISTS product_template_attributes (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    attribute_id UUID NOT NULL REFERENCES product_attributes(id) ON DELETE CASCADE,
    sort_order INTEGER DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(product_id, attribute_id)
);

-- =====================================================
-- PRODUCT TEMPLATE ATTRIBUTE VALUES (which values are available for a product)
-- =====================================================
CREATE TABLE IF NOT EXISTS product_template_attribute_values (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    product_template_attribute_id UUID NOT NULL REFERENCES product_template_attributes(id) ON DELETE CASCADE,
    attribute_value_id UUID NOT NULL REFERENCES product_attribute_values(id) ON DELETE CASCADE,
    price_extra DECIMAL(20, 4) DEFAULT 0, -- extra price for this value
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(product_template_attribute_id, attribute_value_id)
);

-- =====================================================
-- PRODUCT VARIANTS (actual sellable/purchasable items)
-- =====================================================
CREATE TABLE IF NOT EXISTS product_variants (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE, -- parent product template

    -- Variant identification
    sku VARCHAR(100),
    barcode VARCHAR(100),
    internal_reference VARCHAR(100),

    -- Variant-specific pricing (overrides template if set)
    cost_price DECIMAL(20, 4),
    list_price DECIMAL(20, 4),

    -- Variant-specific stock settings
    weight DECIMAL(20, 4),
    volume DECIMAL(20, 4),

    -- Status
    is_active BOOLEAN DEFAULT TRUE,

    -- Combination name (auto-generated, e.g., "Red / Large")
    variant_name VARCHAR(255),

    -- Full name including product name (e.g., "T-Shirt (Red / Large)")
    display_name VARCHAR(500),

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,

    UNIQUE(tenant_id, sku)
);

-- =====================================================
-- PRODUCT VARIANT ATTRIBUTE VALUES (which attribute values make up this variant)
-- =====================================================
CREATE TABLE IF NOT EXISTS product_variant_attribute_values (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    variant_id UUID NOT NULL REFERENCES product_variants(id) ON DELETE CASCADE,
    template_attribute_value_id UUID NOT NULL REFERENCES product_template_attribute_values(id) ON DELETE CASCADE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(variant_id, template_attribute_value_id)
);

-- =====================================================
-- ADD has_variants FLAG TO PRODUCTS TABLE
-- =====================================================
ALTER TABLE products ADD COLUMN IF NOT EXISTS has_variants BOOLEAN DEFAULT FALSE;

-- =====================================================
-- ADD variant_id TO INVENTORY TABLE
-- =====================================================
ALTER TABLE inventory ADD COLUMN IF NOT EXISTS variant_id UUID REFERENCES product_variants(id) ON DELETE CASCADE;

-- =====================================================
-- ADD variant_id TO SALES ORDER LINES
-- =====================================================
ALTER TABLE sales_order_lines ADD COLUMN IF NOT EXISTS variant_id UUID REFERENCES product_variants(id) ON DELETE SET NULL;

-- =====================================================
-- ADD variant_id TO PURCHASE ORDER LINES
-- =====================================================
ALTER TABLE purchase_order_lines ADD COLUMN IF NOT EXISTS variant_id UUID REFERENCES product_variants(id) ON DELETE SET NULL;

-- =====================================================
-- ADD variant_id TO INVENTORY TRANSACTIONS
-- =====================================================
ALTER TABLE inventory_transactions ADD COLUMN IF NOT EXISTS variant_id UUID REFERENCES product_variants(id) ON DELETE SET NULL;

-- =====================================================
-- INDEXES
-- =====================================================
CREATE INDEX IF NOT EXISTS idx_product_attributes_tenant ON product_attributes(tenant_id);
CREATE INDEX IF NOT EXISTS idx_product_attribute_values_attribute ON product_attribute_values(attribute_id);
CREATE INDEX IF NOT EXISTS idx_product_template_attributes_product ON product_template_attributes(product_id);
CREATE INDEX IF NOT EXISTS idx_product_template_attribute_values_pta ON product_template_attribute_values(product_template_attribute_id);
CREATE INDEX IF NOT EXISTS idx_product_variants_product ON product_variants(product_id);
CREATE INDEX IF NOT EXISTS idx_product_variants_tenant ON product_variants(tenant_id);
CREATE INDEX IF NOT EXISTS idx_product_variants_sku ON product_variants(tenant_id, sku);
CREATE INDEX IF NOT EXISTS idx_product_variants_barcode ON product_variants(barcode);
CREATE INDEX IF NOT EXISTS idx_product_variant_attr_values_variant ON product_variant_attribute_values(variant_id);
CREATE INDEX IF NOT EXISTS idx_inventory_variant ON inventory(variant_id);
CREATE INDEX IF NOT EXISTS idx_sales_order_lines_variant ON sales_order_lines(variant_id);
CREATE INDEX IF NOT EXISTS idx_purchase_order_lines_variant ON purchase_order_lines(variant_id);

-- =====================================================
-- PERMISSIONS
-- =====================================================
INSERT INTO permissions (id, module, resource, action, description)
VALUES
    (uuid_generate_v4(), 'inventory', 'product_attribute', 'read', 'View product attributes'),
    (uuid_generate_v4(), 'inventory', 'product_attribute', 'create', 'Create product attributes'),
    (uuid_generate_v4(), 'inventory', 'product_attribute', 'update', 'Update product attributes'),
    (uuid_generate_v4(), 'inventory', 'product_attribute', 'delete', 'Delete product attributes'),
    (uuid_generate_v4(), 'inventory', 'product_variant', 'read', 'View product variants'),
    (uuid_generate_v4(), 'inventory', 'product_variant', 'create', 'Create product variants'),
    (uuid_generate_v4(), 'inventory', 'product_variant', 'update', 'Update product variants'),
    (uuid_generate_v4(), 'inventory', 'product_variant', 'delete', 'Delete product variants')
ON CONFLICT DO NOTHING;
