-- Materials assigned to construction sub-stages
CREATE TABLE IF NOT EXISTS construction_sub_stage_materials (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL,
    sub_stage_id BIGINT NOT NULL REFERENCES construction_sub_stages(id) ON DELETE CASCADE,
    product_id UUID,
    product_name VARCHAR(500) NOT NULL,
    uom VARCHAR(50) NOT NULL DEFAULT 'шт',
    quantity DECIMAL(18,4) NOT NULL DEFAULT 0,
    unit_cost DECIMAL(18,4) NOT NULL DEFAULT 0,
    total_cost DECIMAL(18,4) NOT NULL DEFAULT 0,
    created_date TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_date TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sub_stage_materials_sub_stage ON construction_sub_stage_materials(sub_stage_id);
CREATE INDEX IF NOT EXISTS idx_sub_stage_materials_tenant ON construction_sub_stage_materials(tenant_id);
