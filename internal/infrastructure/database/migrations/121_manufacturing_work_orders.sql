-- =====================================================
-- MANUFACTURING WORK ORDERS & INVENTORY TRANSFERS (Odoo-like)
-- =====================================================
-- Work Orders: Individual orders for each operation in a Manufacturing Order
-- Manufacturing Transfers: Pick components & Store finished products

-- =====================================================
-- 1. MANUFACTURING STEPS CONFIGURATION
-- =====================================================

-- Add manufacturing steps to warehouses (1, 2, or 3 step manufacturing)
ALTER TABLE warehouses ADD COLUMN IF NOT EXISTS manufacturing_steps INTEGER DEFAULT 1;
COMMENT ON COLUMN warehouses.manufacturing_steps IS 'Manufacturing steps: 1=simple, 2=pick+produce, 3=pick+produce+store';

-- Add production location to warehouses
ALTER TABLE warehouses ADD COLUMN IF NOT EXISTS production_location_id UUID REFERENCES warehouse_locations(id);
COMMENT ON COLUMN warehouses.production_location_id IS 'Default location for production/manufacturing';

-- =====================================================
-- 2. WORK ORDERS TABLE
-- =====================================================

CREATE TABLE IF NOT EXISTS work_orders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,

    -- Link to Manufacturing Order
    production_order_id UUID NOT NULL REFERENCES production_orders(id) ON DELETE CASCADE,

    -- Work Order Info
    work_order_number VARCHAR(50) NOT NULL,
    name VARCHAR(255) NOT NULL,
    sequence INTEGER NOT NULL DEFAULT 10,

    -- Operation details (from BOM routing)
    bom_operation_id UUID REFERENCES bom_operations(id) ON DELETE SET NULL,
    operation_name VARCHAR(255),

    -- Work Center
    work_center_id UUID REFERENCES work_centers(id) ON DELETE SET NULL,
    work_center_name VARCHAR(255),

    -- Quantity
    quantity_to_produce DECIMAL(15,4) NOT NULL DEFAULT 1,
    quantity_produced DECIMAL(15,4) DEFAULT 0,

    -- Time tracking
    expected_duration_minutes INTEGER DEFAULT 0,
    setup_time_minutes INTEGER DEFAULT 0,
    actual_duration_minutes INTEGER DEFAULT 0,

    -- Dates
    scheduled_start TIMESTAMP WITH TIME ZONE,
    scheduled_end TIMESTAMP WITH TIME ZONE,
    actual_start TIMESTAMP WITH TIME ZONE,
    actual_end TIMESTAMP WITH TIME ZONE,

    -- Status: draft, waiting, ready, in_progress, done, cancelled
    status VARCHAR(30) NOT NULL DEFAULT 'draft',

    -- Operator
    operator_id UUID REFERENCES users(id) ON DELETE SET NULL,
    operator_name VARCHAR(255),

    -- Quality
    quality_check_required BOOLEAN DEFAULT false,
    quality_check_passed BOOLEAN,

    -- Notes
    instructions TEXT,
    notes TEXT,

    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);

-- Work order sequence
CREATE SEQUENCE IF NOT EXISTS work_order_seq START 1;

-- Indexes
CREATE INDEX IF NOT EXISTS idx_work_orders_tenant ON work_orders(tenant_id);
CREATE INDEX IF NOT EXISTS idx_work_orders_org ON work_orders(organization_id);
CREATE INDEX IF NOT EXISTS idx_work_orders_production ON work_orders(production_order_id);
CREATE INDEX IF NOT EXISTS idx_work_orders_work_center ON work_orders(work_center_id);
CREATE INDEX IF NOT EXISTS idx_work_orders_status ON work_orders(status);
CREATE INDEX IF NOT EXISTS idx_work_orders_scheduled ON work_orders(scheduled_start, scheduled_end);

COMMENT ON TABLE work_orders IS 'Individual work orders for each operation in a manufacturing order (Odoo-like)';

-- =====================================================
-- 3. MANUFACTURING TRANSFERS TABLE
-- =====================================================

CREATE TABLE IF NOT EXISTS manufacturing_transfers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,

    -- Link to Manufacturing Order
    production_order_id UUID NOT NULL REFERENCES production_orders(id) ON DELETE CASCADE,

    -- Transfer Info
    transfer_number VARCHAR(50) NOT NULL,
    transfer_type VARCHAR(30) NOT NULL, -- 'pick_components', 'store_finished'

    -- Locations
    source_location_id UUID REFERENCES warehouse_locations(id),
    destination_location_id UUID REFERENCES warehouse_locations(id),
    warehouse_id UUID REFERENCES warehouses(id),

    -- Status: draft, waiting, ready, done, cancelled
    status VARCHAR(30) NOT NULL DEFAULT 'draft',

    -- Dates
    scheduled_date TIMESTAMP WITH TIME ZONE,
    done_date TIMESTAMP WITH TIME ZONE,

    -- Processed by
    processed_by UUID REFERENCES users(id) ON DELETE SET NULL,

    notes TEXT,

    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);

-- Transfer sequences
CREATE SEQUENCE IF NOT EXISTS mfg_transfer_seq START 1;

-- Indexes
CREATE INDEX IF NOT EXISTS idx_mfg_transfers_tenant ON manufacturing_transfers(tenant_id);
CREATE INDEX IF NOT EXISTS idx_mfg_transfers_org ON manufacturing_transfers(organization_id);
CREATE INDEX IF NOT EXISTS idx_mfg_transfers_production ON manufacturing_transfers(production_order_id);
CREATE INDEX IF NOT EXISTS idx_mfg_transfers_type ON manufacturing_transfers(transfer_type);
CREATE INDEX IF NOT EXISTS idx_mfg_transfers_status ON manufacturing_transfers(status);

COMMENT ON TABLE manufacturing_transfers IS 'Inventory transfers for manufacturing (pick components, store finished)';

-- =====================================================
-- 4. MANUFACTURING TRANSFER LINES
-- =====================================================

CREATE TABLE IF NOT EXISTS manufacturing_transfer_lines (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    manufacturing_transfer_id UUID NOT NULL REFERENCES manufacturing_transfers(id) ON DELETE CASCADE,

    -- Product
    product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    product_variant_id UUID REFERENCES product_variants(id) ON DELETE SET NULL,

    -- Quantity
    quantity_demanded DECIMAL(15,4) NOT NULL DEFAULT 0,
    quantity_done DECIMAL(15,4) DEFAULT 0,
    uom VARCHAR(30),

    -- Lot tracking
    lot_id UUID,
    lot_number VARCHAR(100),
    serial_number VARCHAR(100),

    -- Locations
    source_location_id UUID REFERENCES warehouse_locations(id),
    destination_location_id UUID REFERENCES warehouse_locations(id),

    notes TEXT,

    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mfg_transfer_lines_transfer ON manufacturing_transfer_lines(manufacturing_transfer_id);
CREATE INDEX IF NOT EXISTS idx_mfg_transfer_lines_product ON manufacturing_transfer_lines(product_id);

-- =====================================================
-- 5. WORK ORDER TIME LOGS
-- =====================================================

CREATE TABLE IF NOT EXISTS work_order_time_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    work_order_id UUID NOT NULL REFERENCES work_orders(id) ON DELETE CASCADE,

    -- Time tracking
    start_time TIMESTAMP WITH TIME ZONE NOT NULL,
    end_time TIMESTAMP WITH TIME ZONE,
    duration_minutes INTEGER,

    -- What was done
    log_type VARCHAR(30) NOT NULL DEFAULT 'production', -- 'setup', 'production', 'pause', 'cleanup'
    quantity_produced DECIMAL(15,4) DEFAULT 0,

    -- Who
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    user_name VARCHAR(255),

    -- Quality issues
    scrap_quantity DECIMAL(15,4) DEFAULT 0,
    scrap_reason TEXT,

    notes TEXT,

    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_wo_time_logs_work_order ON work_order_time_logs(work_order_id);
CREATE INDEX IF NOT EXISTS idx_wo_time_logs_user ON work_order_time_logs(user_id);

COMMENT ON TABLE work_order_time_logs IS 'Time tracking for work orders (start/stop/pause)';

-- =====================================================
-- 6. ADD LINKS TO PRODUCTION ORDERS
-- =====================================================

-- Add transfer links to production orders
ALTER TABLE production_orders ADD COLUMN IF NOT EXISTS pick_transfer_id UUID REFERENCES manufacturing_transfers(id);
ALTER TABLE production_orders ADD COLUMN IF NOT EXISTS store_transfer_id UUID REFERENCES manufacturing_transfers(id);

COMMENT ON COLUMN production_orders.pick_transfer_id IS 'Link to pick components transfer (2/3-step manufacturing)';
COMMENT ON COLUMN production_orders.store_transfer_id IS 'Link to store finished products transfer (3-step manufacturing)';

-- =====================================================
-- 7. TRIGGERS
-- =====================================================

-- Auto-update updated_at
CREATE OR REPLACE FUNCTION update_work_orders_timestamp()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trigger_work_orders_updated ON work_orders;
CREATE TRIGGER trigger_work_orders_updated
    BEFORE UPDATE ON work_orders
    FOR EACH ROW
    EXECUTE FUNCTION update_work_orders_timestamp();

DROP TRIGGER IF EXISTS trigger_mfg_transfers_updated ON manufacturing_transfers;
CREATE TRIGGER trigger_mfg_transfers_updated
    BEFORE UPDATE ON manufacturing_transfers
    FOR EACH ROW
    EXECUTE FUNCTION update_work_orders_timestamp();
