-- Migration: Add warehouse operation steps (Odoo-style)
-- This allows configuring 1-step, 2-step, or 3-step warehouse operations for receiving and shipping

-- Add reception and delivery steps columns to warehouses table
ALTER TABLE warehouses
ADD COLUMN IF NOT EXISTS reception_steps INTEGER DEFAULT 1 CHECK (reception_steps IN (1, 2, 3)),
ADD COLUMN IF NOT EXISTS delivery_steps INTEGER DEFAULT 1 CHECK (delivery_steps IN (1, 2, 3));

-- Add comments explaining the step options
COMMENT ON COLUMN warehouses.reception_steps IS '1=Receive goods directly to stock, 2=Receive to input then stock, 3=Receive to input, quality, then stock';
COMMENT ON COLUMN warehouses.delivery_steps IS '1=Deliver directly from stock, 2=Pick then deliver, 3=Pick, pack, then deliver';

-- Create operation types table for warehouse operations
CREATE TABLE IF NOT EXISTS warehouse_operation_types (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    warehouse_id UUID NOT NULL REFERENCES warehouses(id) ON DELETE CASCADE,
    code VARCHAR(50) NOT NULL,
    name VARCHAR(255) NOT NULL,
    operation_type VARCHAR(50) NOT NULL CHECK (operation_type IN ('incoming', 'outgoing', 'internal')),
    sequence INTEGER DEFAULT 10,
    source_location_id UUID REFERENCES warehouse_locations(id),
    destination_location_id UUID REFERENCES warehouse_locations(id),
    return_operation_type_id UUID REFERENCES warehouse_operation_types(id),
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(warehouse_id, code)
);

CREATE INDEX IF NOT EXISTS idx_warehouse_operation_types_warehouse ON warehouse_operation_types(warehouse_id);
CREATE INDEX IF NOT EXISTS idx_warehouse_operation_types_tenant ON warehouse_operation_types(tenant_id);

-- Create stock picking table (transfer orders)
CREATE TABLE IF NOT EXISTS stock_pickings (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    origin VARCHAR(255), -- Source document (e.g., PO number, SO number)
    operation_type_id UUID NOT NULL REFERENCES warehouse_operation_types(id),
    source_location_id UUID REFERENCES warehouse_locations(id),
    destination_location_id UUID REFERENCES warehouse_locations(id),
    scheduled_date TIMESTAMP WITH TIME ZONE,
    date_done TIMESTAMP WITH TIME ZONE,
    state VARCHAR(50) DEFAULT 'draft' CHECK (state IN ('draft', 'waiting', 'confirmed', 'assigned', 'done', 'cancelled')),
    priority VARCHAR(20) DEFAULT 'normal' CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
    backorder_id UUID REFERENCES stock_pickings(id), -- Reference to backorder if partial
    note TEXT,
    user_id UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_stock_pickings_tenant ON stock_pickings(tenant_id);
CREATE INDEX IF NOT EXISTS idx_stock_pickings_operation_type ON stock_pickings(operation_type_id);
CREATE INDEX IF NOT EXISTS idx_stock_pickings_state ON stock_pickings(state);
CREATE INDEX IF NOT EXISTS idx_stock_pickings_scheduled ON stock_pickings(scheduled_date);

-- Create stock move table (individual product movements)
CREATE TABLE IF NOT EXISTS stock_moves (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    picking_id UUID REFERENCES stock_pickings(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES products(id),
    product_uom_qty DECIMAL(20, 4) NOT NULL, -- Quantity in product UOM
    qty_done DECIMAL(20, 4) DEFAULT 0, -- Actually moved quantity
    source_location_id UUID NOT NULL REFERENCES warehouse_locations(id),
    destination_location_id UUID NOT NULL REFERENCES warehouse_locations(id),
    lot_id UUID REFERENCES inventory_lots(id),
    state VARCHAR(50) DEFAULT 'draft' CHECK (state IN ('draft', 'waiting', 'confirmed', 'assigned', 'done', 'cancelled')),
    date TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    reference VARCHAR(255),
    origin VARCHAR(255),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_stock_moves_picking ON stock_moves(picking_id);
CREATE INDEX IF NOT EXISTS idx_stock_moves_product ON stock_moves(product_id);
CREATE INDEX IF NOT EXISTS idx_stock_moves_tenant ON stock_moves(tenant_id);
CREATE INDEX IF NOT EXISTS idx_stock_moves_state ON stock_moves(state);

-- Create function to auto-create default warehouse locations based on steps
CREATE OR REPLACE FUNCTION create_warehouse_locations_for_steps(
    p_warehouse_id UUID,
    p_tenant_id UUID,
    p_reception_steps INTEGER,
    p_delivery_steps INTEGER
) RETURNS VOID AS $$
DECLARE
    v_stock_location_id UUID;
BEGIN
    -- Create main stock location if not exists
    INSERT INTO warehouse_locations (warehouse_id, code, name, type, is_active)
    VALUES (p_warehouse_id, 'STOCK', 'Stock', 'storage', true)
    ON CONFLICT DO NOTHING
    RETURNING id INTO v_stock_location_id;

    IF v_stock_location_id IS NULL THEN
        SELECT id INTO v_stock_location_id FROM warehouse_locations
        WHERE warehouse_id = p_warehouse_id AND code = 'STOCK';
    END IF;

    -- Reception locations based on steps
    IF p_reception_steps >= 2 THEN
        INSERT INTO warehouse_locations (warehouse_id, code, name, type, is_active)
        VALUES (p_warehouse_id, 'INPUT', 'Input Zone', 'receiving', true)
        ON CONFLICT DO NOTHING;
    END IF;

    IF p_reception_steps = 3 THEN
        INSERT INTO warehouse_locations (warehouse_id, code, name, type, is_active)
        VALUES (p_warehouse_id, 'QC', 'Quality Control', 'quality', true)
        ON CONFLICT DO NOTHING;
    END IF;

    -- Delivery locations based on steps
    IF p_delivery_steps >= 2 THEN
        INSERT INTO warehouse_locations (warehouse_id, code, name, type, is_active)
        VALUES (p_warehouse_id, 'OUTPUT', 'Output Zone', 'shipping', true)
        ON CONFLICT DO NOTHING;
    END IF;

    IF p_delivery_steps = 3 THEN
        INSERT INTO warehouse_locations (warehouse_id, code, name, type, is_active)
        VALUES (p_warehouse_id, 'PACK', 'Packing Zone', 'staging', true)
        ON CONFLICT DO NOTHING;
    END IF;
END;
$$ LANGUAGE plpgsql;

-- Add location type 'quality' to the existing types
ALTER TABLE warehouse_locations
ALTER COLUMN type TYPE VARCHAR(50);

-- Update column comment
COMMENT ON COLUMN warehouse_locations.type IS 'Location type: storage, receiving, shipping, staging, quality, returns';
