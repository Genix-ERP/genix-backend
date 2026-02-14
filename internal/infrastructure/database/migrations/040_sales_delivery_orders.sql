-- Migration: Sales Delivery Orders
-- Adds tables for managing outbound deliveries from sales orders
-- Similar to goods_receipts but for outbound stock movements

-- Delivery Orders (outbound shipments from sales orders)
CREATE TABLE IF NOT EXISTS sales_delivery_orders (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    delivery_number VARCHAR(50) NOT NULL,
    sales_order_id UUID NOT NULL REFERENCES sales_orders(id),
    so_number VARCHAR(50),
    customer_id UUID REFERENCES contacts(id),
    customer_name VARCHAR(255),
    delivery_date DATE NOT NULL,
    scheduled_date DATE,
    warehouse_id UUID REFERENCES warehouses(id),

    -- Shipping info
    shipping_method VARCHAR(100),
    tracking_number VARCHAR(100),
    carrier VARCHAR(255),

    -- Status: draft, ready, shipped, delivered, cancelled
    status VARCHAR(30) DEFAULT 'draft',

    notes TEXT,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,

    UNIQUE(tenant_id, delivery_number)
);

-- Delivery Order Line Items
CREATE TABLE IF NOT EXISTS sales_delivery_order_lines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    delivery_order_id UUID NOT NULL REFERENCES sales_delivery_orders(id) ON DELETE CASCADE,
    so_line_id UUID REFERENCES sales_order_lines(id),
    product_id UUID NOT NULL REFERENCES products(id),
    product_name VARCHAR(255),
    quantity_ordered DECIMAL(20, 4) NOT NULL,
    quantity_to_deliver DECIMAL(20, 4) NOT NULL,
    quantity_delivered DECIMAL(20, 4) DEFAULT 0,
    unit_price DECIMAL(20, 4),
    warehouse_id UUID REFERENCES warehouses(id),
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Indexes for fast lookup
CREATE INDEX IF NOT EXISTS idx_sdo_tenant ON sales_delivery_orders(tenant_id);
CREATE INDEX IF NOT EXISTS idx_sdo_so ON sales_delivery_orders(sales_order_id);
CREATE INDEX IF NOT EXISTS idx_sdo_status ON sales_delivery_orders(status);
CREATE INDEX IF NOT EXISTS idx_sdo_customer ON sales_delivery_orders(customer_id);
CREATE INDEX IF NOT EXISTS idx_sdo_warehouse ON sales_delivery_orders(warehouse_id);
CREATE INDEX IF NOT EXISTS idx_sdo_date ON sales_delivery_orders(delivery_date);
CREATE INDEX IF NOT EXISTS idx_sdol_do ON sales_delivery_order_lines(delivery_order_id);
CREATE INDEX IF NOT EXISTS idx_sdol_product ON sales_delivery_order_lines(product_id);
CREATE INDEX IF NOT EXISTS idx_sdol_so_line ON sales_delivery_order_lines(so_line_id);

-- =====================================================
-- ADD SALES DELIVERY PERMISSIONS
-- =====================================================

-- Sales Delivery permissions
INSERT INTO permissions (id, module, resource, action, description)
SELECT gen_random_uuid(), 'sales', 'delivery', a.action,
    'Permission to ' || a.action || ' sales delivery orders'
FROM (VALUES ('read'), ('create'), ('update'), ('delete')) AS a(action)
WHERE NOT EXISTS (
    SELECT 1 FROM permissions WHERE module = 'sales' AND resource = 'delivery' AND action = a.action
);

-- Grant delivery permissions to admin role
INSERT INTO role_permissions (role_id, permission_id, granted_at)
SELECT r.id, p.id, NOW()
FROM roles r
CROSS JOIN permissions p
WHERE r.name = 'admin'
  AND p.module = 'sales'
  AND p.resource = 'delivery'
  AND NOT EXISTS (
    SELECT 1 FROM role_permissions rp WHERE rp.role_id = r.id AND rp.permission_id = p.id
  );

-- Grant delivery permissions to manager role (if exists)
INSERT INTO role_permissions (role_id, permission_id, granted_at)
SELECT r.id, p.id, NOW()
FROM roles r
CROSS JOIN permissions p
WHERE r.name = 'manager'
  AND p.module = 'sales'
  AND p.resource = 'delivery'
  AND NOT EXISTS (
    SELECT 1 FROM role_permissions rp WHERE rp.role_id = r.id AND rp.permission_id = p.id
  );

-- Grant read permission to sales role (if exists)
INSERT INTO role_permissions (role_id, permission_id, granted_at)
SELECT r.id, p.id, NOW()
FROM roles r
CROSS JOIN permissions p
WHERE r.name = 'sales'
  AND p.module = 'sales'
  AND p.resource = 'delivery'
  AND p.action IN ('read', 'create', 'update')
  AND NOT EXISTS (
    SELECT 1 FROM role_permissions rp WHERE rp.role_id = r.id AND rp.permission_id = p.id
  );
