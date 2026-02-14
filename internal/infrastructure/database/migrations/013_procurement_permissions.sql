-- Migration: 012_procurement_permissions
-- Description: Add missing permissions for procurement module (RFQ, contracts, requisitions, receipts, returns)

-- =====================================================
-- ADD PROCUREMENT PERMISSIONS
-- =====================================================

-- RFQ (Request for Quotation) permissions
INSERT INTO permissions (id, module, resource, action, description)
SELECT gen_random_uuid(), 'purchase', 'rfq', a.action,
    'Permission to ' || a.action || ' request for quotations'
FROM (VALUES ('read'), ('create'), ('update'), ('delete')) AS a(action)
WHERE NOT EXISTS (
    SELECT 1 FROM permissions WHERE module = 'purchase' AND resource = 'rfq' AND action = a.action
);

-- Contract permissions
INSERT INTO permissions (id, module, resource, action, description)
SELECT gen_random_uuid(), 'purchase', 'contract', a.action,
    'Permission to ' || a.action || ' procurement contracts'
FROM (VALUES ('read'), ('create'), ('update'), ('delete')) AS a(action)
WHERE NOT EXISTS (
    SELECT 1 FROM permissions WHERE module = 'purchase' AND resource = 'contract' AND action = a.action
);

-- Purchase Requisition permissions
INSERT INTO permissions (id, module, resource, action, description)
SELECT gen_random_uuid(), 'purchase', 'requisition', a.action,
    'Permission to ' || a.action || ' purchase requisitions'
FROM (VALUES ('read'), ('create'), ('update'), ('delete'), ('approve')) AS a(action)
WHERE NOT EXISTS (
    SELECT 1 FROM permissions WHERE module = 'purchase' AND resource = 'requisition' AND action = a.action
);

-- Goods Receipt permissions
INSERT INTO permissions (id, module, resource, action, description)
SELECT gen_random_uuid(), 'purchase', 'receipt', a.action,
    'Permission to ' || a.action || ' goods receipts'
FROM (VALUES ('read'), ('create'), ('update'), ('delete')) AS a(action)
WHERE NOT EXISTS (
    SELECT 1 FROM permissions WHERE module = 'purchase' AND resource = 'receipt' AND action = a.action
);

-- Purchase Return permissions
INSERT INTO permissions (id, module, resource, action, description)
SELECT gen_random_uuid(), 'purchase', 'return', a.action,
    'Permission to ' || a.action || ' purchase returns'
FROM (VALUES ('read'), ('create'), ('update'), ('delete'), ('approve')) AS a(action)
WHERE NOT EXISTS (
    SELECT 1 FROM permissions WHERE module = 'purchase' AND resource = 'return' AND action = a.action
);

-- Settings permissions
INSERT INTO permissions (id, module, resource, action, description)
SELECT gen_random_uuid(), 'settings', 'tenant', a.action,
    'Permission to ' || a.action || ' tenant settings'
FROM (VALUES ('read'), ('update')) AS a(action)
WHERE NOT EXISTS (
    SELECT 1 FROM permissions WHERE module = 'settings' AND resource = 'tenant' AND action = a.action
);

-- AI permissions
INSERT INTO permissions (id, module, resource, action, description)
SELECT gen_random_uuid(), 'ai', 'conversation', a.action,
    'Permission to ' || a.action || ' AI conversations'
FROM (VALUES ('create'), ('read'), ('delete')) AS a(action)
WHERE NOT EXISTS (
    SELECT 1 FROM permissions WHERE module = 'ai' AND resource = 'conversation' AND action = a.action
);

-- Audit permissions
INSERT INTO permissions (id, module, resource, action, description)
SELECT gen_random_uuid(), 'audit', 'log', a.action,
    'Permission to ' || a.action || ' audit logs'
FROM (VALUES ('read'), ('export')) AS a(action)
WHERE NOT EXISTS (
    SELECT 1 FROM permissions WHERE module = 'audit' AND resource = 'log' AND action = a.action
);

-- Finance additional permissions (expenses, assets)
INSERT INTO permissions (id, module, resource, action, description)
SELECT gen_random_uuid(), 'finance', 'expense', a.action,
    'Permission to ' || a.action || ' expenses'
FROM (VALUES ('read'), ('create'), ('update'), ('delete'), ('approve')) AS a(action)
WHERE NOT EXISTS (
    SELECT 1 FROM permissions WHERE module = 'finance' AND resource = 'expense' AND action = a.action
);

INSERT INTO permissions (id, module, resource, action, description)
SELECT gen_random_uuid(), 'finance', 'asset', a.action,
    'Permission to ' || a.action || ' fixed assets'
FROM (VALUES ('read'), ('create'), ('update'), ('delete'), ('approve')) AS a(action)
WHERE NOT EXISTS (
    SELECT 1 FROM permissions WHERE module = 'finance' AND resource = 'asset' AND action = a.action
);

-- Projects permissions
INSERT INTO permissions (id, module, resource, action, description)
SELECT gen_random_uuid(), 'projects', r.resource, a.action,
    'Permission to ' || a.action || ' ' || REPLACE(r.resource, '_', ' ')
FROM (VALUES ('project'), ('task'), ('milestone'), ('time_entry')) AS r(resource)
CROSS JOIN (VALUES ('read'), ('create'), ('update'), ('delete')) AS a(action)
WHERE NOT EXISTS (
    SELECT 1 FROM permissions WHERE module = 'projects' AND resource = r.resource AND action = a.action
);

-- HR additional permissions (payroll)
INSERT INTO permissions (id, module, resource, action, description)
SELECT gen_random_uuid(), 'hr', 'payroll', a.action,
    'Permission to ' || a.action || ' payroll'
FROM (VALUES ('read'), ('create'), ('update'), ('delete'), ('approve')) AS a(action)
WHERE NOT EXISTS (
    SELECT 1 FROM permissions WHERE module = 'hr' AND resource = 'payroll' AND action = a.action
);

-- =====================================================
-- GRANT ALL NEW PERMISSIONS TO ADMIN ROLES
-- =====================================================

-- Grant all new permissions to system_admin role
INSERT INTO role_permissions (role_id, permission_id, granted_at)
SELECT r.id, p.id, NOW()
FROM roles r
CROSS JOIN permissions p
WHERE r.code = 'system_admin'
  AND NOT EXISTS (
    SELECT 1 FROM role_permissions rp WHERE rp.role_id = r.id AND rp.permission_id = p.id
  );

-- Grant all new permissions to admin role
INSERT INTO role_permissions (role_id, permission_id, granted_at)
SELECT r.id, p.id, NOW()
FROM roles r
CROSS JOIN permissions p
WHERE r.code = 'admin'
  AND NOT EXISTS (
    SELECT 1 FROM role_permissions rp WHERE rp.role_id = r.id AND rp.permission_id = p.id
  );

-- Grant read permissions to manager role
INSERT INTO role_permissions (role_id, permission_id, granted_at)
SELECT r.id, p.id, NOW()
FROM roles r
CROSS JOIN permissions p
WHERE r.code = 'manager'
  AND p.action = 'read'
  AND NOT EXISTS (
    SELECT 1 FROM role_permissions rp WHERE rp.role_id = r.id AND rp.permission_id = p.id
  );

-- Grant purchase module permissions to purchaser role
INSERT INTO role_permissions (role_id, permission_id, granted_at)
SELECT r.id, p.id, NOW()
FROM roles r
CROSS JOIN permissions p
WHERE r.code = 'purchaser'
  AND p.module = 'purchase'
  AND NOT EXISTS (
    SELECT 1 FROM role_permissions rp WHERE rp.role_id = r.id AND rp.permission_id = p.id
  );

-- Grant basic read access to regular user role
INSERT INTO role_permissions (role_id, permission_id, granted_at)
SELECT r.id, p.id, NOW()
FROM roles r
CROSS JOIN permissions p
WHERE r.code = 'user'
  AND p.action = 'read'
  AND p.module IN ('inventory', 'sales', 'purchase')
  AND NOT EXISTS (
    SELECT 1 FROM role_permissions rp WHERE rp.role_id = r.id AND rp.permission_id = p.id
  );
