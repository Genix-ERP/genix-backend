-- Migration: Fix owner role column for tenant owners
-- Description: Sets role='owner' for users who are assigned the Owner role
--              but whose role column was never updated (defaulted to 'employee')
-- Date: 2026-04-03

-- Set role='owner' for users who have the system Owner role assigned
UPDATE users u
SET role = 'owner'
FROM user_roles ur
INNER JOIN roles r ON r.id = ur.role_id
WHERE ur.user_id = u.id
AND r.tenant_id = u.tenant_id
AND r.code = 'owner'
AND r.is_system = true
AND u.role != 'owner';

-- Also re-link any users to employees by matching email (in case employee was created after user)
UPDATE users u
SET employee_id = e.id
FROM employees e
WHERE u.email = e.email
AND u.tenant_id = e.tenant_id
AND u.employee_id IS NULL
AND e.deleted_at IS NULL
AND u.deleted_at IS NULL;

-- Also link by phone for users without email match
UPDATE users u
SET employee_id = e.id
FROM employees e
WHERE u.phone IS NOT NULL
AND u.phone != ''
AND u.phone = e.phone
AND u.tenant_id = e.tenant_id
AND u.employee_id IS NULL
AND e.deleted_at IS NULL
AND u.deleted_at IS NULL;
