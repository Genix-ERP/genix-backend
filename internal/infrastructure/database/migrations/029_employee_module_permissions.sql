-- Migration: Create employee_module_permissions table
-- Description: Stores granular module-level CRUD permissions for employees
-- Date: 2026-01-23

-- Create employee_module_permissions table
CREATE TABLE IF NOT EXISTS employee_module_permissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    module_id VARCHAR(50) NOT NULL,
    can_create BOOLEAN NOT NULL DEFAULT false,
    can_read BOOLEAN NOT NULL DEFAULT false,
    can_update BOOLEAN NOT NULL DEFAULT false,
    can_delete BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    -- Ensure unique permission per employee per module within a tenant
    UNIQUE(tenant_id, employee_id, module_id)
);

-- Create indexes for faster queries
CREATE INDEX idx_emp_mod_perms_tenant ON employee_module_permissions(tenant_id);
CREATE INDEX idx_emp_mod_perms_employee ON employee_module_permissions(employee_id);
CREATE INDEX idx_emp_mod_perms_module ON employee_module_permissions(module_id);
CREATE INDEX idx_emp_mod_perms_tenant_employee ON employee_module_permissions(tenant_id, employee_id);

-- Add comments
COMMENT ON TABLE employee_module_permissions IS 'Stores granular module-level CRUD permissions for employees';
COMMENT ON COLUMN employee_module_permissions.module_id IS 'Module identifier: dashboard, inventory, customers, financials, hr, etc.';
COMMENT ON COLUMN employee_module_permissions.can_create IS 'Permission to create new records in the module';
COMMENT ON COLUMN employee_module_permissions.can_read IS 'Permission to view/read records in the module';
COMMENT ON COLUMN employee_module_permissions.can_update IS 'Permission to edit/update records in the module';
COMMENT ON COLUMN employee_module_permissions.can_delete IS 'Permission to delete records in the module';
