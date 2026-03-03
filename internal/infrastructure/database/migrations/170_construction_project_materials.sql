-- Create construction_project_materials table to track materials used per project
CREATE TABLE IF NOT EXISTS construction_project_materials (
    id          BIGSERIAL PRIMARY KEY,
    tenant_id   UUID NOT NULL,
    project_id  BIGINT NOT NULL,
    product_id  UUID NOT NULL,
    product_name VARCHAR(255) NOT NULL DEFAULT '',
    uom          VARCHAR(50)  NOT NULL DEFAULT '',
    approved_quantity NUMERIC(15, 4) NOT NULL DEFAULT 0,
    unit_cost    NUMERIC(15, 4) NOT NULL DEFAULT 0,
    created_date TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_date TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, project_id, product_id)
);

CREATE INDEX IF NOT EXISTS idx_construction_project_materials_project
    ON construction_project_materials (tenant_id, project_id);
