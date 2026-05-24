-- +migrate Up
CREATE TABLE IF NOT EXISTS production_split_output_materials (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    split_output_id UUID NOT NULL REFERENCES production_split_outputs(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES products(id),
    quantity_per_piece DECIMAL(15,4) NOT NULL,
    total_quantity DECIMAL(15,4) NOT NULL,
    unit_cost DECIMAL(15,4) NOT NULL DEFAULT 0,
    total_cost DECIMAL(15,4) NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_split_output_materials_split ON production_split_output_materials(split_output_id);
