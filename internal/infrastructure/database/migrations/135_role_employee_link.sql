-- Add role_id column to employees for direct role-to-employee mapping
ALTER TABLE employees ADD COLUMN IF NOT EXISTS role_id UUID REFERENCES roles(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_employees_role_id ON employees(role_id);
