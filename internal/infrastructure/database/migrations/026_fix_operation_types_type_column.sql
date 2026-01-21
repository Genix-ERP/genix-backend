-- Migration: Fix warehouse_operation_types - add missing type column
-- The table was created previously without the type column

-- Add the type column if it doesn't exist
DO $$
BEGIN
    -- Check if column exists, if not add it
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'warehouse_operation_types' AND column_name = 'type'
    ) THEN
        ALTER TABLE warehouse_operation_types
        ADD COLUMN type VARCHAR(20) NOT NULL DEFAULT 'custom';

        RAISE NOTICE 'Added type column to warehouse_operation_types';
    END IF;
END $$;

-- Create index on type column if it doesn't exist
CREATE INDEX IF NOT EXISTS idx_warehouse_operation_types_type
ON warehouse_operation_types(type) WHERE deleted_at IS NULL;

-- Update existing rows to have proper type based on code pattern
UPDATE warehouse_operation_types
SET type = CASE
    WHEN code LIKE '%/IN' OR code LIKE '%/IN/%' THEN 'receipt'
    WHEN code LIKE '%/INT' OR code LIKE '%/INT/%' THEN 'internal'
    WHEN code LIKE '%/OUT' OR code LIKE '%/OUT/%' THEN 'delivery'
    WHEN code LIKE '%/POS' OR code LIKE '%/POS/%' THEN 'pos'
    ELSE 'custom'
END
WHERE type = 'custom' OR type IS NULL;

-- Now recreate the function (it should work now that column exists)
CREATE OR REPLACE FUNCTION create_default_operation_types(
    p_tenant_id UUID,
    p_warehouse_id UUID,
    p_warehouse_code VARCHAR
) RETURNS VOID AS $$
BEGIN
    -- 1. Receipts (Поступления)
    INSERT INTO warehouse_operation_types (
        tenant_id, warehouse_id, code, name, type, sequence, color, show_operations
    ) VALUES (
        p_tenant_id, p_warehouse_id,
        p_warehouse_code || '/IN',
        'Receipts',
        'receipt',
        1,
        '#22c55e', -- green
        true
    ) ON CONFLICT (tenant_id, warehouse_id, code) DO NOTHING;

    -- 2. Internal Transfers (Внутренние переводы)
    INSERT INTO warehouse_operation_types (
        tenant_id, warehouse_id, code, name, type, sequence, color, show_operations
    ) VALUES (
        p_tenant_id, p_warehouse_id,
        p_warehouse_code || '/INT',
        'Internal Transfers',
        'internal',
        2,
        '#3b82f6', -- blue
        true
    ) ON CONFLICT (tenant_id, warehouse_id, code) DO NOTHING;

    -- 3. Delivery Orders (Запросы на доставку)
    INSERT INTO warehouse_operation_types (
        tenant_id, warehouse_id, code, name, type, sequence, color, show_operations
    ) VALUES (
        p_tenant_id, p_warehouse_id,
        p_warehouse_code || '/OUT',
        'Delivery Orders',
        'delivery',
        3,
        '#f97316', -- orange
        true
    ) ON CONFLICT (tenant_id, warehouse_id, code) DO NOTHING;

    -- 4. PoS Orders (POS заказы)
    INSERT INTO warehouse_operation_types (
        tenant_id, warehouse_id, code, name, type, sequence, color, show_operations
    ) VALUES (
        p_tenant_id, p_warehouse_id,
        p_warehouse_code || '/POS',
        'PoS Orders',
        'pos',
        4,
        '#a855f7', -- purple
        true
    ) ON CONFLICT (tenant_id, warehouse_id, code) DO NOTHING;
END;
$$ LANGUAGE plpgsql;

-- Create default operation types for existing warehouses that don't have them
DO $$
DECLARE
    wh RECORD;
BEGIN
    FOR wh IN SELECT id, tenant_id, code FROM warehouses WHERE deleted_at IS NULL LOOP
        -- Check if this warehouse already has operation types
        IF NOT EXISTS (SELECT 1 FROM warehouse_operation_types WHERE warehouse_id = wh.id AND deleted_at IS NULL) THEN
            PERFORM create_default_operation_types(wh.tenant_id, wh.id, wh.code);
        END IF;
    END LOOP;
END $$;
