-- Migration: Add employee_id to users table
-- Description: Links users to their employee records for permission checking
-- Date: 2026-01-23

-- Add employee_id column to users table
ALTER TABLE users
ADD COLUMN IF NOT EXISTS employee_id UUID REFERENCES employees(id) ON DELETE SET NULL;

-- Create index for faster lookups
CREATE INDEX IF NOT EXISTS idx_users_employee ON users(employee_id);

-- Add role column if not exists (for owner, site_admin, admin, employee roles)
ALTER TABLE users
ADD COLUMN IF NOT EXISTS role VARCHAR(20) DEFAULT 'employee';

-- Create index on role
CREATE INDEX IF NOT EXISTS idx_users_role ON users(role);

-- Update existing users: Link users to employees by matching email
-- This will connect existing users with their employee records
UPDATE users u
SET employee_id = e.id
FROM employees e
WHERE u.email = e.email
AND u.tenant_id = e.tenant_id
AND u.employee_id IS NULL
AND e.deleted_at IS NULL;

-- Comment on columns
COMMENT ON COLUMN users.employee_id IS 'Link to employee record for permission management';
COMMENT ON COLUMN users.role IS 'User role: owner, site_admin, admin, employee';
