-- Fix permissions table - add missing columns before dropshipping/payment_terms migrations
-- This migration adds 'code' and 'name' columns that are used by subsequent migrations

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
