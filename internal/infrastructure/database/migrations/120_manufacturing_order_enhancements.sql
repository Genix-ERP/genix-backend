-- Migration 118: Manufacturing Order Enhancements
-- Adds fields for Gazablok/Plita/Beton manufacturing processes:
-- - mold_count: Number of molds used (1 mold = 12 blocks for Gazablok)
-- - shift: Day/Night shift tracking
-- - current_stage: Current production stage (mixing, rising, drying, cutting, packing, done)
-- - package_count: Number of packages produced (40-46 blocks per package)
-- - good_quantity: Quantity passed quality control (after cutting)
-- - reject_quantity: Scrap/Brak quantity at cutting stage

-- Add new columns to production_orders table
ALTER TABLE production_orders ADD COLUMN IF NOT EXISTS mold_count INTEGER DEFAULT 0;
ALTER TABLE production_orders ADD COLUMN IF NOT EXISTS shift VARCHAR(20);
ALTER TABLE production_orders ADD COLUMN IF NOT EXISTS current_stage VARCHAR(50) DEFAULT 'draft';
ALTER TABLE production_orders ADD COLUMN IF NOT EXISTS package_count INTEGER DEFAULT 0;
ALTER TABLE production_orders ADD COLUMN IF NOT EXISTS good_quantity DECIMAL(15,4) DEFAULT 0;
ALTER TABLE production_orders ADD COLUMN IF NOT EXISTS reject_quantity DECIMAL(15,4) DEFAULT 0;

-- Add comments for documentation
COMMENT ON COLUMN production_orders.mold_count IS 'Number of molds used in production (for Gazablok: 1 mold = 12 blocks)';
COMMENT ON COLUMN production_orders.shift IS 'Production shift: day or night';
COMMENT ON COLUMN production_orders.current_stage IS 'Current manufacturing stage: draft, mixing, rising, drying, cutting, packing, done';
COMMENT ON COLUMN production_orders.package_count IS 'Number of packages produced (for Gazablok: 40-46 blocks per package)';
COMMENT ON COLUMN production_orders.good_quantity IS 'Quantity that passed quality control';
COMMENT ON COLUMN production_orders.reject_quantity IS 'Rejected/scrap quantity (brak)';

-- Create production order stages table for detailed stage tracking
CREATE TABLE IF NOT EXISTS production_order_stages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    production_order_id UUID NOT NULL REFERENCES production_orders(id) ON DELETE CASCADE,
    stage_name VARCHAR(50) NOT NULL,
    sequence INTEGER NOT NULL DEFAULT 0,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    started_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE,
    operator_id UUID REFERENCES users(id) ON DELETE SET NULL,
    operator_name VARCHAR(255),
    duration_minutes INTEGER DEFAULT 0,
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);

-- Create indexes for production_order_stages
CREATE INDEX IF NOT EXISTS idx_production_order_stages_tenant ON production_order_stages(tenant_id);
CREATE INDEX IF NOT EXISTS idx_production_order_stages_order ON production_order_stages(production_order_id);
CREATE INDEX IF NOT EXISTS idx_production_order_stages_status ON production_order_stages(status);

-- Add comments
COMMENT ON TABLE production_order_stages IS 'Tracks individual manufacturing stages for production orders';
COMMENT ON COLUMN production_order_stages.stage_name IS 'Stage name: mixing, rising, drying, cutting, packing, done';
COMMENT ON COLUMN production_order_stages.status IS 'Stage status: pending, in_progress, completed, skipped';

-- Create production output records table for tracking output at each stage
CREATE TABLE IF NOT EXISTS production_outputs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    production_order_id UUID NOT NULL REFERENCES production_orders(id) ON DELETE CASCADE,
    stage_id UUID REFERENCES production_order_stages(id) ON DELETE SET NULL,
    output_type VARCHAR(20) NOT NULL DEFAULT 'good',
    quantity DECIMAL(15,4) NOT NULL DEFAULT 0,
    uom VARCHAR(20) DEFAULT 'units',
    package_count INTEGER DEFAULT 0,
    notes TEXT,
    recorded_by UUID REFERENCES users(id) ON DELETE SET NULL,
    recorded_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);

-- Create indexes for production_outputs
CREATE INDEX IF NOT EXISTS idx_production_outputs_tenant ON production_outputs(tenant_id);
CREATE INDEX IF NOT EXISTS idx_production_outputs_order ON production_outputs(production_order_id);
CREATE INDEX IF NOT EXISTS idx_production_outputs_type ON production_outputs(output_type);

COMMENT ON TABLE production_outputs IS 'Records production output quantities at each stage';
COMMENT ON COLUMN production_outputs.output_type IS 'Output type: good, reject, scrap, rework';
