-- Migration: Add permission column to employees table
-- Description: Adds permission level field (learner, basic, important, grant)
-- Date: 2026-01-14

-- Add permission column to employees table
ALTER TABLE employees
ADD COLUMN permission VARCHAR(20);

-- Add comment to the column
COMMENT ON COLUMN employees.permission IS 'Employee permission level: learner, basic, important, grant';

-- Create index for faster queries on permission
CREATE INDEX idx_employees_permission ON employees(permission);
