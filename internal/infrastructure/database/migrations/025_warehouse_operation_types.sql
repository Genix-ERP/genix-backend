-- Migration: Warehouse Operation Types (Odoo-style)
-- Each warehouse can have multiple operation types (Receipts, Internal Transfers, Delivery, POS Orders)

-- Create operation types enum
DO $$ BEGIN
    CREATE TYPE operation_type_code AS ENUM ('receipt', 'internal', 'delivery', 'pos', 'custom');
EXCEPTION
    WHEN duplicate_object THEN null;
END $$;

-- Create warehouse operation types table
CREATE TABLE IF NOT EXISTS warehouse_operation_types (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    warehouse_id UUID NOT NULL REFERENCES warehouses(id) ON DELETE CASCADE,
    code VARCHAR(50) NOT NULL,
    name VARCHAR(255) NOT NULL,
    type VARCHAR(20) NOT NULL DEFAULT 'custom', -- receipt, internal, delivery, pos, custom
    sequence INTEGER DEFAULT 10,
    -- Source and destination locations
    default_location_src_id UUID REFERENCES warehouse_locations(id),
    default_location_dest_id UUID REFERENCES warehouse_locations(id),
    -- Operation settings
    show_operations BOOLEAN DEFAULT true, -- Show in dashboard
    show_reserved BOOLEAN DEFAULT true,
    barcode_enabled BOOLEAN DEFAULT true,
    create_backorder BOOLEAN DEFAULT true, -- Auto-create backorder for partial transfers
    reservation_method VARCHAR(20) DEFAULT 'at_confirm', -- at_confirm, manual, by_date
    -- Chained operations (for multi-step flows)
    return_picking_type_id UUID REFERENCES warehouse_operation_types(id),
    -- Colors for Kanban view (hex color codes)
    color VARCHAR(7) DEFAULT '#6366f1',
    -- Counters for dashboard
    count_picking_ready INTEGER DEFAULT 0,
    count_picking_late INTEGER DEFAULT 0,
    count_picking_waiting INTEGER DEFAULT 0,
    count_picking_backorders INTEGER DEFAULT 0,
    -- Timestamps
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    -- Constraints
    UNIQUE(tenant_id, warehouse_id, code)
);

-- Add comments
COMMENT ON TABLE warehouse_operation_types IS 'Operation types for warehouse operations (Receipts, Internal Transfers, Delivery Orders, etc.)';
COMMENT ON COLUMN warehouse_operation_types.type IS 'Type of operation: receipt, internal, delivery, pos, custom';
COMMENT ON COLUMN warehouse_operation_types.sequence IS 'Display order in menus and Kanban';
COMMENT ON COLUMN warehouse_operation_types.default_location_src_id IS 'Default source location for this operation type';
COMMENT ON COLUMN warehouse_operation_types.default_location_dest_id IS 'Default destination location for this operation type';
COMMENT ON COLUMN warehouse_operation_types.reservation_method IS 'When to reserve stock: at_confirm, manual, by_date';
COMMENT ON COLUMN warehouse_operation_types.return_picking_type_id IS 'Operation type to use for returns';

-- Create indexes
CREATE INDEX IF NOT EXISTS idx_warehouse_operation_types_tenant ON warehouse_operation_types(tenant_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_warehouse_operation_types_warehouse ON warehouse_operation_types(warehouse_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_warehouse_operation_types_type ON warehouse_operation_types(type) WHERE deleted_at IS NULL;

-- Add operation_type_id to stock_pickings table if it exists
DO $$ BEGIN
    ALTER TABLE inventory_transactions ADD COLUMN IF NOT EXISTS operation_type_id UUID REFERENCES warehouse_operation_types(id);
EXCEPTION
    WHEN undefined_table THEN null;
END $$;

-- Function to create default operation types for a new warehouse
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
