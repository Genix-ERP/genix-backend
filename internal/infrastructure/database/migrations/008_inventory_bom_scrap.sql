-- Migration: 007_inventory_bom_scrap.sql
-- Description: Add Bill of Materials (BOM) and Scrap Management tables
-- Date: 2026-01-15

-- =====================================================
-- BILL OF MATERIALS (BOM) TABLES
-- =====================================================

-- 1. Product BOMs (Bill of Materials header)
CREATE TABLE IF NOT EXISTS product_boms (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    code VARCHAR(50) NOT NULL,
    name VARCHAR(255) NOT NULL,
    product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    bom_type VARCHAR(20) DEFAULT 'manufacturing', -- manufacturing, kit, phantom
    quantity DECIMAL(20, 4) DEFAULT 1, -- Output quantity
    version INTEGER DEFAULT 1,
    is_active BOOLEAN DEFAULT true,
    is_default BOOLEAN DEFAULT false,
    effective_date DATE,
    expiry_date DATE,
    notes TEXT,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, code)
);

-- 2. BOM Lines (Components)
CREATE TABLE IF NOT EXISTS bom_lines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    bom_id UUID NOT NULL REFERENCES product_boms(id) ON DELETE CASCADE,
    line_number INTEGER NOT NULL,
    component_id UUID NOT NULL REFERENCES products(id),
    quantity DECIMAL(20, 4) NOT NULL,
    unit_id UUID REFERENCES units_of_measure(id),
    unit_of_measure VARCHAR(20) DEFAULT 'pcs',
    scrap_percent DECIMAL(5, 2) DEFAULT 0, -- Expected scrap percentage
    is_optional BOOLEAN DEFAULT false,
    substitute_component_id UUID REFERENCES products(id), -- Alternative component
    operation_sequence INTEGER DEFAULT 10, -- For manufacturing routing
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 3. BOM Operations (for manufacturing routing - optional)
CREATE TABLE IF NOT EXISTS bom_operations (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    bom_id UUID NOT NULL REFERENCES product_boms(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL,
    operation_name VARCHAR(255) NOT NULL,
    work_center VARCHAR(100),
    setup_time_minutes DECIMAL(10, 2) DEFAULT 0,
    run_time_minutes DECIMAL(10, 2) DEFAULT 0,
    labor_cost DECIMAL(20, 4) DEFAULT 0,
    overhead_cost DECIMAL(20, 4) DEFAULT 0,
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- =====================================================
-- SCRAP MANAGEMENT TABLES
-- =====================================================

-- 4. Scrap Reasons (predefined reasons)
CREATE TABLE IF NOT EXISTS scrap_reasons (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    code VARCHAR(50) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    is_active BOOLEAN DEFAULT true,
    requires_approval BOOLEAN DEFAULT false,
    account_id UUID REFERENCES accounts(id), -- Expense account for scrapped inventory
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, code)
);

-- 5. Scrap Orders (main scrap records)
CREATE TABLE IF NOT EXISTS scrap_orders (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    scrap_number VARCHAR(50) NOT NULL,
    product_id UUID NOT NULL REFERENCES products(id),
    warehouse_id UUID NOT NULL REFERENCES warehouses(id),
    location_id UUID REFERENCES warehouse_locations(id),
    lot_id UUID REFERENCES inventory_lots(id),
    quantity DECIMAL(20, 4) NOT NULL,
    unit_cost DECIMAL(20, 4),
    total_cost DECIMAL(20, 4),
    scrap_reason_id UUID REFERENCES scrap_reasons(id),
    reason VARCHAR(50), -- damaged, expired, defective, obsolete, quality_issue, other
    reason_notes TEXT,
    scrap_date DATE NOT NULL,
    status VARCHAR(20) DEFAULT 'draft', -- draft, pending_approval, approved, completed, cancelled
    scrapped_by UUID REFERENCES users(id),
    approved_by UUID REFERENCES users(id),
    approved_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE,
    journal_entry_id UUID REFERENCES journal_entries(id),
    inventory_transaction_id UUID REFERENCES inventory_transactions(id),
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, scrap_number)
);

-- =====================================================
-- REORDER RULES TABLE (extends existing alerts)
-- =====================================================

-- 6. Reorder Rules (automated reorder configuration)
CREATE TABLE IF NOT EXISTS reorder_rules (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES products(id),
    warehouse_id UUID REFERENCES warehouses(id), -- NULL means all warehouses
    min_qty DECIMAL(20, 4) NOT NULL,
    max_qty DECIMAL(20, 4),
    reorder_qty DECIMAL(20, 4) NOT NULL,
    trigger_type VARCHAR(20) DEFAULT 'min_qty', -- min_qty, forecast, schedule
    preferred_vendor_id UUID REFERENCES contacts(id),
    lead_time_days INTEGER DEFAULT 0,
    safety_stock DECIMAL(20, 4) DEFAULT 0,
    auto_create_po BOOLEAN DEFAULT false,
    is_active BOOLEAN DEFAULT true,
    last_triggered_at TIMESTAMP WITH TIME ZONE,
    notes TEXT,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, product_id, warehouse_id)
);

-- =====================================================
-- INDEXES
-- =====================================================

-- BOM indexes
CREATE INDEX IF NOT EXISTS idx_product_boms_tenant ON product_boms(tenant_id);
CREATE INDEX IF NOT EXISTS idx_product_boms_product ON product_boms(product_id);
CREATE INDEX IF NOT EXISTS idx_product_boms_type ON product_boms(bom_type);
CREATE INDEX IF NOT EXISTS idx_product_boms_active ON product_boms(is_active);
CREATE INDEX IF NOT EXISTS idx_bom_lines_bom ON bom_lines(bom_id);
CREATE INDEX IF NOT EXISTS idx_bom_lines_component ON bom_lines(component_id);
CREATE INDEX IF NOT EXISTS idx_bom_operations_bom ON bom_operations(bom_id);

-- Scrap indexes
CREATE INDEX IF NOT EXISTS idx_scrap_reasons_tenant ON scrap_reasons(tenant_id);
CREATE INDEX IF NOT EXISTS idx_scrap_orders_tenant ON scrap_orders(tenant_id);
CREATE INDEX IF NOT EXISTS idx_scrap_orders_product ON scrap_orders(product_id);
CREATE INDEX IF NOT EXISTS idx_scrap_orders_warehouse ON scrap_orders(warehouse_id);
CREATE INDEX IF NOT EXISTS idx_scrap_orders_status ON scrap_orders(status);
CREATE INDEX IF NOT EXISTS idx_scrap_orders_date ON scrap_orders(scrap_date);
CREATE INDEX IF NOT EXISTS idx_scrap_orders_reason ON scrap_orders(reason);

-- Reorder rules indexes
CREATE INDEX IF NOT EXISTS idx_reorder_rules_tenant ON reorder_rules(tenant_id);
CREATE INDEX IF NOT EXISTS idx_reorder_rules_product ON reorder_rules(product_id);
CREATE INDEX IF NOT EXISTS idx_reorder_rules_warehouse ON reorder_rules(warehouse_id);
CREATE INDEX IF NOT EXISTS idx_reorder_rules_active ON reorder_rules(is_active);

-- =====================================================
-- TRIGGERS
-- =====================================================

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_product_boms_updated_at') THEN
        CREATE TRIGGER update_product_boms_updated_at BEFORE UPDATE ON product_boms
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_bom_lines_updated_at') THEN
        CREATE TRIGGER update_bom_lines_updated_at BEFORE UPDATE ON bom_lines
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_bom_operations_updated_at') THEN
        CREATE TRIGGER update_bom_operations_updated_at BEFORE UPDATE ON bom_operations
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_scrap_reasons_updated_at') THEN
        CREATE TRIGGER update_scrap_reasons_updated_at BEFORE UPDATE ON scrap_reasons
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_scrap_orders_updated_at') THEN
        CREATE TRIGGER update_scrap_orders_updated_at BEFORE UPDATE ON scrap_orders
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_reorder_rules_updated_at') THEN
        CREATE TRIGGER update_reorder_rules_updated_at BEFORE UPDATE ON reorder_rules
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;
END $$;

-- =====================================================
-- SEED DEFAULT SCRAP REASONS
-- =====================================================

-- Note: These will be inserted per tenant, this is just for reference
-- INSERT INTO scrap_reasons (tenant_id, code, name, description) VALUES
-- (tenant_id, 'DAMAGED', 'Damaged', 'Product damaged during handling or storage'),
-- (tenant_id, 'EXPIRED', 'Expired', 'Product past expiration date'),
-- (tenant_id, 'DEFECTIVE', 'Defective', 'Manufacturing defect or quality issue'),
-- (tenant_id, 'OBSOLETE', 'Obsolete', 'Product no longer in use or discontinued'),
-- (tenant_id, 'QUALITY', 'Quality Issue', 'Failed quality inspection'),
-- (tenant_id, 'OTHER', 'Other', 'Other reason');
