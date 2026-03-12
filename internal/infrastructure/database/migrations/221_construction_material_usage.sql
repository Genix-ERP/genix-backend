-- Material usage tracking for construction projects
CREATE TABLE IF NOT EXISTS construction_material_usage (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL,
    project_id BIGINT NOT NULL REFERENCES construction_projects(id) ON DELETE CASCADE,
    building_id BIGINT REFERENCES construction_buildings(id) ON DELETE SET NULL,
    wbs_id BIGINT REFERENCES construction_wbs(id) ON DELETE SET NULL,
    product_id UUID,
    product_name VARCHAR(500) NOT NULL,
    uom VARCHAR(50) NOT NULL,
    quantity_used DECIMAL(18,4) NOT NULL,
    usage_date DATE NOT NULL,
    estimate_line_id BIGINT REFERENCES construction_estimate_line(id) ON DELETE SET NULL,
    recorded_by UUID,
    notes TEXT,
    created_date TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_date TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_material_usage_project ON construction_material_usage(project_id);
CREATE INDEX IF NOT EXISTS idx_material_usage_wbs ON construction_material_usage(wbs_id);
CREATE INDEX IF NOT EXISTS idx_material_usage_date ON construction_material_usage(usage_date);
CREATE INDEX IF NOT EXISTS idx_material_usage_tenant ON construction_material_usage(tenant_id);
