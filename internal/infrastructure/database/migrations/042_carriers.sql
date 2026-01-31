-- Migration: Carriers
-- Adds table for managing shipping carriers

CREATE TABLE IF NOT EXISTS carriers (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    code VARCHAR(50) NOT NULL,
    name VARCHAR(255) NOT NULL,
    tracking_url VARCHAR(500),  -- URL template for tracking, e.g. https://track.dhl.com/?id={tracking_number}
    phone VARCHAR(50),
    email VARCHAR(255),
    website VARCHAR(255),
    notes TEXT,
    is_active BOOLEAN DEFAULT true,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, code)
);

CREATE INDEX IF NOT EXISTS idx_carriers_tenant ON carriers(tenant_id);
CREATE INDEX IF NOT EXISTS idx_carriers_active ON carriers(tenant_id, is_active);

-- Add some default carriers for each tenant
INSERT INTO carriers (id, tenant_id, code, name, tracking_url, website, is_active)
SELECT
    uuid_generate_v4(),
    t.id,
    c.code,
    c.name,
    c.tracking_url,
    c.website,
    true
FROM tenants t
CROSS JOIN (VALUES
    ('DHL', 'DHL Express', 'https://www.dhl.com/track?tracking-id={tracking_number}', 'https://www.dhl.com'),
    ('FEDEX', 'FedEx', 'https://www.fedex.com/fedextrack/?trknbr={tracking_number}', 'https://www.fedex.com'),
    ('UPS', 'UPS', 'https://www.ups.com/track?tracknum={tracking_number}', 'https://www.ups.com'),
    ('USPS', 'USPS', 'https://tools.usps.com/go/TrackConfirmAction?tLabels={tracking_number}', 'https://www.usps.com'),
    ('LOCAL', 'Local Delivery', NULL, NULL)
) AS c(code, name, tracking_url, website)
WHERE NOT EXISTS (
    SELECT 1 FROM carriers ca WHERE ca.tenant_id = t.id AND ca.code = c.code
);

-- Add carrier permissions
INSERT INTO permissions (id, module, resource, action, description)
SELECT gen_random_uuid(), 'inventory', 'carrier', a.action,
    'Permission to ' || a.action || ' carriers'
FROM (VALUES ('read'), ('create'), ('update'), ('delete')) AS a(action)
WHERE NOT EXISTS (
    SELECT 1 FROM permissions WHERE module = 'inventory' AND resource = 'carrier' AND action = a.action
);

-- Grant carrier permissions to admin role
INSERT INTO role_permissions (role_id, permission_id, granted_at)
SELECT r.id, p.id, NOW()
FROM roles r
CROSS JOIN permissions p
WHERE r.name = 'admin'
  AND p.module = 'inventory'
  AND p.resource = 'carrier'
  AND NOT EXISTS (
    SELECT 1 FROM role_permissions rp WHERE rp.role_id = r.id AND rp.permission_id = p.id
  );
