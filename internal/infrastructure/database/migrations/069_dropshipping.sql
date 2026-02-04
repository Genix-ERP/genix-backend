-- Dropshipping Module Migration
-- Enables direct supplier-to-customer fulfillment without warehouse intermediary

-- Add dropshipping fields to products
ALTER TABLE products ADD COLUMN IF NOT EXISTS is_dropshippable BOOLEAN DEFAULT false;
ALTER TABLE products ADD COLUMN IF NOT EXISTS dropship_only BOOLEAN DEFAULT false;
ALTER TABLE products ADD COLUMN IF NOT EXISTS default_dropship_vendor_id UUID REFERENCES contacts(id);

-- Add dropshipping fields to sales orders
ALTER TABLE sales_orders ADD COLUMN IF NOT EXISTS has_dropship_items BOOLEAN DEFAULT false;
ALTER TABLE sales_orders ADD COLUMN IF NOT EXISTS dropship_status VARCHAR(30);

-- Add dropshipping fields to sales order lines
ALTER TABLE sales_order_lines ADD COLUMN IF NOT EXISTS is_dropship BOOLEAN DEFAULT false;
ALTER TABLE sales_order_lines ADD COLUMN IF NOT EXISTS dropship_vendor_id UUID REFERENCES contacts(id);
ALTER TABLE sales_order_lines ADD COLUMN IF NOT EXISTS dropship_status VARCHAR(30) DEFAULT 'pending';
ALTER TABLE sales_order_lines ADD COLUMN IF NOT EXISTS dropship_po_id UUID REFERENCES purchase_orders(id);
ALTER TABLE sales_order_lines ADD COLUMN IF NOT EXISTS dropship_po_line_id UUID;

-- Dropship orders table - links sales orders to purchase orders for dropshipping
CREATE TABLE IF NOT EXISTS dropship_orders (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Link to sales order
    sales_order_id UUID NOT NULL REFERENCES sales_orders(id) ON DELETE CASCADE,
    sales_order_number VARCHAR(50),

    -- Link to purchase order (auto-created for vendor)
    purchase_order_id UUID REFERENCES purchase_orders(id),
    purchase_order_number VARCHAR(50),

    -- Vendor information
    vendor_id UUID NOT NULL REFERENCES contacts(id),
    vendor_name VARCHAR(255),

    -- Customer information (for shipping)
    customer_id UUID NOT NULL REFERENCES contacts(id),
    customer_name VARCHAR(255),
    shipping_address JSONB,

    -- Status tracking
    status VARCHAR(30) NOT NULL DEFAULT 'pending', -- pending, po_created, sent_to_vendor, shipped, delivered, cancelled

    -- Shipping information
    shipping_method VARCHAR(100),
    tracking_number VARCHAR(255),
    carrier VARCHAR(100),
    estimated_delivery DATE,
    shipped_date TIMESTAMP WITH TIME ZONE,
    delivered_date TIMESTAMP WITH TIME ZONE,

    -- Financial
    subtotal DECIMAL(15,2) DEFAULT 0,
    shipping_cost DECIMAL(15,2) DEFAULT 0,
    total_cost DECIMAL(15,2) DEFAULT 0,
    margin DECIMAL(15,2) DEFAULT 0,
    margin_percent DECIMAL(5,2) DEFAULT 0,

    -- Notes
    vendor_notes TEXT,
    internal_notes TEXT,
    customer_notes TEXT,

    -- Timestamps
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Dropship order lines
CREATE TABLE IF NOT EXISTS dropship_order_lines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    dropship_order_id UUID NOT NULL REFERENCES dropship_orders(id) ON DELETE CASCADE,

    -- Link to sales order line
    sales_order_line_id UUID NOT NULL REFERENCES sales_order_lines(id),

    -- Link to purchase order line (once PO is created)
    purchase_order_line_id UUID,

    -- Product information
    product_id UUID REFERENCES products(id),
    product_name VARCHAR(255),
    sku VARCHAR(100),
    description TEXT,

    -- Quantities
    quantity DECIMAL(15,4) NOT NULL,
    quantity_shipped DECIMAL(15,4) DEFAULT 0,
    quantity_delivered DECIMAL(15,4) DEFAULT 0,

    -- Pricing
    vendor_price DECIMAL(15,2) NOT NULL, -- Cost from vendor
    sale_price DECIMAL(15,2) NOT NULL,   -- Price to customer
    line_cost DECIMAL(15,2),             -- vendor_price * quantity
    line_revenue DECIMAL(15,2),          -- sale_price * quantity
    line_margin DECIMAL(15,2),           -- Revenue - Cost

    -- Status
    status VARCHAR(30) DEFAULT 'pending', -- pending, ordered, shipped, delivered, cancelled

    -- Notes
    notes TEXT,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Dropship vendor settings - per-vendor dropshipping configuration
CREATE TABLE IF NOT EXISTS dropship_vendor_settings (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    vendor_id UUID NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,

    -- Vendor dropship capabilities
    is_dropship_enabled BOOLEAN DEFAULT true,
    auto_send_orders BOOLEAN DEFAULT false,

    -- Communication preferences
    notification_email VARCHAR(255),
    order_format VARCHAR(50) DEFAULT 'standard', -- standard, csv, edi, api

    -- Lead times and shipping
    default_lead_time_days INTEGER DEFAULT 3,
    ships_direct_to_customer BOOLEAN DEFAULT true,
    provides_tracking BOOLEAN DEFAULT true,

    -- Pricing rules
    markup_type VARCHAR(20) DEFAULT 'percentage', -- percentage, fixed
    default_markup DECIMAL(10,2) DEFAULT 20,

    -- Minimum order requirements
    min_order_value DECIMAL(15,2),
    min_order_quantity INTEGER,

    -- API integration (for future use)
    api_endpoint VARCHAR(500),
    api_key_encrypted TEXT,

    -- Notes
    notes TEXT,

    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(tenant_id, vendor_id)
);

-- Dropship product vendors - which vendors can dropship which products
CREATE TABLE IF NOT EXISTS dropship_product_vendors (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    vendor_id UUID NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,

    -- Pricing
    vendor_price DECIMAL(15,2),
    currency VARCHAR(10) DEFAULT 'UZS',

    -- Lead time
    lead_time_days INTEGER DEFAULT 3,

    -- Availability
    is_available BOOLEAN DEFAULT true,
    min_order_qty DECIMAL(15,4) DEFAULT 1,

    -- Priority (lower = higher priority)
    priority INTEGER DEFAULT 100,

    -- Vendor's SKU for this product
    vendor_sku VARCHAR(100),

    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(tenant_id, product_id, vendor_id)
);

-- Indexes for performance
CREATE INDEX IF NOT EXISTS idx_dropship_orders_tenant ON dropship_orders(tenant_id);
CREATE INDEX IF NOT EXISTS idx_dropship_orders_sales_order ON dropship_orders(sales_order_id);
CREATE INDEX IF NOT EXISTS idx_dropship_orders_purchase_order ON dropship_orders(purchase_order_id);
CREATE INDEX IF NOT EXISTS idx_dropship_orders_vendor ON dropship_orders(vendor_id);
CREATE INDEX IF NOT EXISTS idx_dropship_orders_status ON dropship_orders(status);

CREATE INDEX IF NOT EXISTS idx_dropship_lines_order ON dropship_order_lines(dropship_order_id);
CREATE INDEX IF NOT EXISTS idx_dropship_lines_so_line ON dropship_order_lines(sales_order_line_id);
CREATE INDEX IF NOT EXISTS idx_dropship_lines_product ON dropship_order_lines(product_id);

CREATE INDEX IF NOT EXISTS idx_dropship_vendor_settings_tenant ON dropship_vendor_settings(tenant_id);
CREATE INDEX IF NOT EXISTS idx_dropship_vendor_settings_vendor ON dropship_vendor_settings(vendor_id);

CREATE INDEX IF NOT EXISTS idx_dropship_product_vendors_tenant ON dropship_product_vendors(tenant_id);
CREATE INDEX IF NOT EXISTS idx_dropship_product_vendors_product ON dropship_product_vendors(product_id);
CREATE INDEX IF NOT EXISTS idx_dropship_product_vendors_vendor ON dropship_product_vendors(vendor_id);

CREATE INDEX IF NOT EXISTS idx_products_dropshippable ON products(is_dropshippable) WHERE is_dropshippable = true;
CREATE INDEX IF NOT EXISTS idx_products_dropship_vendor ON products(default_dropship_vendor_id) WHERE default_dropship_vendor_id IS NOT NULL;

-- Add dropshipping permissions
INSERT INTO permissions (id, module, resource, action, description, code, name)
VALUES
    (uuid_generate_v4(), 'sales', 'dropship', 'read', 'View dropship orders and vendors', 'sales.dropship.read', 'View Dropshipping'),
    (uuid_generate_v4(), 'sales', 'dropship', 'create', 'Create dropship orders', 'sales.dropship.create', 'Create Dropship Orders'),
    (uuid_generate_v4(), 'sales', 'dropship', 'update', 'Update dropship orders', 'sales.dropship.update', 'Update Dropship Orders'),
    (uuid_generate_v4(), 'sales', 'dropship', 'delete', 'Delete dropship orders', 'sales.dropship.delete', 'Delete Dropship Orders')
ON CONFLICT (module, resource, action) DO NOTHING;
