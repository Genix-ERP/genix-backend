-- Fix permissions table - add missing columns and fix failed dropshipping/payment_terms migrations
-- This migration fixes the issue where 068_dropshipping and 069_payment_terms tried to insert
-- into permissions table with 'code' and 'name' columns that don't exist

-- Step 1: Add 'code' column if it doesn't exist
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'permissions' AND column_name = 'code'
    ) THEN
        ALTER TABLE permissions ADD COLUMN code VARCHAR(100);
    END IF;
END $$;

-- Step 2: Add 'name' column if it doesn't exist
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'permissions' AND column_name = 'name'
    ) THEN
        ALTER TABLE permissions ADD COLUMN name VARCHAR(255);
    END IF;
END $$;

-- Step 3: Update existing permissions to have code values (module.resource.action pattern)
UPDATE permissions
SET code = module || '.' || resource || '.' || action
WHERE code IS NULL;

-- Step 4: Update existing permissions to have name values
UPDATE permissions
SET name = INITCAP(REPLACE(action, '_', ' ')) || ' ' || INITCAP(REPLACE(resource, '_', ' '))
WHERE name IS NULL;

-- Step 5: Insert dropshipping permissions (from 068_dropshipping.sql that failed)
INSERT INTO permissions (id, module, resource, action, description, code, name)
VALUES
    (uuid_generate_v4(), 'sales', 'dropship', 'read', 'View dropship orders and vendors', 'sales.dropship.read', 'View Dropshipping'),
    (uuid_generate_v4(), 'sales', 'dropship', 'create', 'Create dropship orders', 'sales.dropship.create', 'Create Dropship Orders'),
    (uuid_generate_v4(), 'sales', 'dropship', 'update', 'Update dropship orders', 'sales.dropship.update', 'Update Dropship Orders'),
    (uuid_generate_v4(), 'sales', 'dropship', 'delete', 'Delete dropship orders', 'sales.dropship.delete', 'Delete Dropship Orders')
ON CONFLICT (module, resource, action) DO NOTHING;

-- Step 6: Insert payment terms permissions (from 069_payment_terms.sql that failed)
INSERT INTO permissions (id, module, resource, action, description, code, name)
VALUES
    (uuid_generate_v4(), 'sales', 'payment_term', 'read', 'View payment terms', 'sales.payment_term.read', 'View Payment Terms'),
    (uuid_generate_v4(), 'sales', 'payment_term', 'create', 'Create payment terms', 'sales.payment_term.create', 'Create Payment Terms'),
    (uuid_generate_v4(), 'sales', 'payment_term', 'update', 'Update payment terms', 'sales.payment_term.update', 'Update Payment Terms'),
    (uuid_generate_v4(), 'sales', 'payment_term', 'delete', 'Delete payment terms', 'sales.payment_term.delete', 'Delete Payment Terms')
ON CONFLICT (module, resource, action) DO NOTHING;
