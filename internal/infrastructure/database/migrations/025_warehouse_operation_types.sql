-- Migration: Warehouse Operation Types (Odoo-style)
-- Each warehouse can have multiple operation types (Receipts, Internal Transfers, Delivery, POS Orders)

-- Create operation types enum (if not exists)
DO $$ BEGIN
    CREATE TYPE operation_type_code AS ENUM ('receipt', 'internal', 'delivery', 'pos', 'custom');
EXCEPTION
    WHEN duplicate_object THEN null;
END $$;

-- Check if table exists and add missing columns
DO $$
BEGIN
    -- If table doesn't exist, create it
    IF NOT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'warehouse_operation_types') THEN
        CREATE TABLE warehouse_operation_types (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            tenant_id UUID NOT NULL REFERENCES tenants(id),
            warehouse_id UUID NOT NULL REFERENCES warehouses(id) ON DELETE CASCADE,
            code VARCHAR(50) NOT NULL,
            name VARCHAR(255) NOT NULL,
            type VARCHAR(20) NOT NULL DEFAULT 'custom',
            sequence INTEGER DEFAULT 10,
            default_location_src_id UUID,
            default_location_dest_id UUID,
            show_operations BOOLEAN DEFAULT true,
            show_reserved BOOLEAN DEFAULT true,
            barcode_enabled BOOLEAN DEFAULT true,
            create_backorder BOOLEAN DEFAULT true,
            reservation_method VARCHAR(20) DEFAULT 'at_confirm',
            return_picking_type_id UUID,
            color VARCHAR(7) DEFAULT '#6366f1',
            count_picking_ready INTEGER DEFAULT 0,
            count_picking_late INTEGER DEFAULT 0,
            count_picking_waiting INTEGER DEFAULT 0,
            count_picking_backorders INTEGER DEFAULT 0,
            is_active BOOLEAN DEFAULT true,
            created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
            deleted_at TIMESTAMP WITH TIME ZONE,
            UNIQUE(tenant_id, warehouse_id, code)
        );
    ELSE
        -- Table exists, add missing columns one by one
        IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'warehouse_operation_types' AND column_name = 'type') THEN
            ALTER TABLE warehouse_operation_types ADD COLUMN type VARCHAR(20) NOT NULL DEFAULT 'custom';
        END IF;

        IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'warehouse_operation_types' AND column_name = 'sequence') THEN
            ALTER TABLE warehouse_operation_types ADD COLUMN sequence INTEGER DEFAULT 10;
        END IF;

        IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'warehouse_operation_types' AND column_name = 'default_location_src_id') THEN
            ALTER TABLE warehouse_operation_types ADD COLUMN default_location_src_id UUID;
        END IF;

        IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'warehouse_operation_types' AND column_name = 'default_location_dest_id') THEN
            ALTER TABLE warehouse_operation_types ADD COLUMN default_location_dest_id UUID;
        END IF;

        IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'warehouse_operation_types' AND column_name = 'show_operations') THEN
            ALTER TABLE warehouse_operation_types ADD COLUMN show_operations BOOLEAN DEFAULT true;
        END IF;

        IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'warehouse_operation_types' AND column_name = 'show_reserved') THEN
            ALTER TABLE warehouse_operation_types ADD COLUMN show_reserved BOOLEAN DEFAULT true;
        END IF;

        IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'warehouse_operation_types' AND column_name = 'barcode_enabled') THEN
            ALTER TABLE warehouse_operation_types ADD COLUMN barcode_enabled BOOLEAN DEFAULT true;
        END IF;

        IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'warehouse_operation_types' AND column_name = 'create_backorder') THEN
            ALTER TABLE warehouse_operation_types ADD COLUMN create_backorder BOOLEAN DEFAULT true;
        END IF;

        IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'warehouse_operation_types' AND column_name = 'reservation_method') THEN
            ALTER TABLE warehouse_operation_types ADD COLUMN reservation_method VARCHAR(20) DEFAULT 'at_confirm';
        END IF;

        IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'warehouse_operation_types' AND column_name = 'return_picking_type_id') THEN
            ALTER TABLE warehouse_operation_types ADD COLUMN return_picking_type_id UUID;
        END IF;

        IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'warehouse_operation_types' AND column_name = 'color') THEN
            ALTER TABLE warehouse_operation_types ADD COLUMN color VARCHAR(7) DEFAULT '#6366f1';
        END IF;

        IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'warehouse_operation_types' AND column_name = 'count_picking_ready') THEN
            ALTER TABLE warehouse_operation_types ADD COLUMN count_picking_ready INTEGER DEFAULT 0;
        END IF;

        IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'warehouse_operation_types' AND column_name = 'count_picking_late') THEN
            ALTER TABLE warehouse_operation_types ADD COLUMN count_picking_late INTEGER DEFAULT 0;
        END IF;

        IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'warehouse_operation_types' AND column_name = 'count_picking_waiting') THEN
            ALTER TABLE warehouse_operation_types ADD COLUMN count_picking_waiting INTEGER DEFAULT 0;
        END IF;

        IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'warehouse_operation_types' AND column_name = 'count_picking_backorders') THEN
            ALTER TABLE warehouse_operation_types ADD COLUMN count_picking_backorders INTEGER DEFAULT 0;
        END IF;

        IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'warehouse_operation_types' AND column_name = 'is_active') THEN
            ALTER TABLE warehouse_operation_types ADD COLUMN is_active BOOLEAN DEFAULT true;
        END IF;

        IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'warehouse_operation_types' AND column_name = 'deleted_at') THEN
            ALTER TABLE warehouse_operation_types ADD COLUMN deleted_at TIMESTAMP WITH TIME ZONE;
        END IF;
    END IF;
END $$;

-- Create indexes (safe - IF NOT EXISTS)
CREATE INDEX IF NOT EXISTS idx_warehouse_operation_types_tenant ON warehouse_operation_types(tenant_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_warehouse_operation_types_warehouse ON warehouse_operation_types(warehouse_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_warehouse_operation_types_type ON warehouse_operation_types(type) WHERE deleted_at IS NULL;

-- Add operation_type_id to inventory_transactions if it exists
DO $$ BEGIN
    ALTER TABLE inventory_transactions ADD COLUMN IF NOT EXISTS operation_type_id UUID;
EXCEPTION
    WHEN undefined_table THEN null;
    WHEN duplicate_column THEN null;
END $$;

-- Update existing rows type based on code pattern
UPDATE warehouse_operation_types
SET type = CASE
    WHEN code LIKE '%/IN' OR code LIKE '%/IN/%' THEN 'receipt'
    WHEN code LIKE '%/INT' OR code LIKE '%/INT/%' THEN 'internal'
    WHEN code LIKE '%/OUT' OR code LIKE '%/OUT/%' THEN 'delivery'
    WHEN code LIKE '%/POS' OR code LIKE '%/POS/%' THEN 'pos'
    ELSE type
END
WHERE type = 'custom' OR type IS NULL;

-- Function to create default operation types for a warehouse
CREATE OR REPLACE FUNCTION create_default_operation_types(
    p_tenant_id UUID,
    p_warehouse_id UUID,
    p_warehouse_code VARCHAR
) RETURNS VOID AS $$
BEGIN
    INSERT INTO warehouse_operation_types (tenant_id, warehouse_id, code, name, type, sequence, color, show_operations)
    VALUES (p_tenant_id, p_warehouse_id, p_warehouse_code || '/IN', 'Receipts', 'receipt', 1, '#22c55e', true)
    ON CONFLICT (tenant_id, warehouse_id, code) DO NOTHING;

    INSERT INTO warehouse_operation_types (tenant_id, warehouse_id, code, name, type, sequence, color, show_operations)
    VALUES (p_tenant_id, p_warehouse_id, p_warehouse_code || '/INT', 'Internal Transfers', 'internal', 2, '#3b82f6', true)
    ON CONFLICT (tenant_id, warehouse_id, code) DO NOTHING;

    INSERT INTO warehouse_operation_types (tenant_id, warehouse_id, code, name, type, sequence, color, show_operations)
    VALUES (p_tenant_id, p_warehouse_id, p_warehouse_code || '/OUT', 'Delivery Orders', 'delivery', 3, '#f97316', true)
    ON CONFLICT (tenant_id, warehouse_id, code) DO NOTHING;

    INSERT INTO warehouse_operation_types (tenant_id, warehouse_id, code, name, type, sequence, color, show_operations)
    VALUES (p_tenant_id, p_warehouse_id, p_warehouse_code || '/POS', 'PoS Orders', 'pos', 4, '#a855f7', true)
    ON CONFLICT (tenant_id, warehouse_id, code) DO NOTHING;
END;
$$ LANGUAGE plpgsql;

-- Create default operation types for existing warehouses
DO $$
DECLARE
    wh RECORD;
BEGIN
    FOR wh IN SELECT id, tenant_id, code FROM warehouses WHERE deleted_at IS NULL LOOP
        IF NOT EXISTS (SELECT 1 FROM warehouse_operation_types WHERE warehouse_id = wh.id AND deleted_at IS NULL) THEN
            PERFORM create_default_operation_types(wh.tenant_id, wh.id, wh.code);
        END IF;
    END LOOP;
END $$;
