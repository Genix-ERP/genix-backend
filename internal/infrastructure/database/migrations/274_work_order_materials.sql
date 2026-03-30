-- Work Order Materials - track materials consumed per work order stage
CREATE TABLE IF NOT EXISTS work_order_materials (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    work_order_id UUID NOT NULL REFERENCES work_orders(id) ON DELETE CASCADE,
    production_order_id UUID NOT NULL REFERENCES production_orders(id),
    product_id UUID NOT NULL REFERENCES products(id),
    product_name VARCHAR(255),
    quantity DECIMAL(15,4) NOT NULL,
    uom VARCHAR(50) DEFAULT 'pcs',
    unit_cost DECIMAL(15,4) DEFAULT 0,
    total_cost DECIMAL(15,4) DEFAULT 0,
    notes TEXT,
    created_by UUID,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_wom_tenant ON work_order_materials(tenant_id);
CREATE INDEX IF NOT EXISTS idx_wom_work_order ON work_order_materials(work_order_id);
CREATE INDEX IF NOT EXISTS idx_wom_production_order ON work_order_materials(production_order_id);
