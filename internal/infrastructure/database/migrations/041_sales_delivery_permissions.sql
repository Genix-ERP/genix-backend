-- Migration: Sales Delivery Permissions
-- Adds permissions for sales delivery orders feature
-- This is safe to run multiple times due to NOT EXISTS checks

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

-- Grant permissions to sales role (if exists)
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
