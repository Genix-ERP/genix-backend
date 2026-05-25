-- +migrate Up
CREATE TABLE IF NOT EXISTS product_packaging_materials (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    material_id UUID NOT NULL REFERENCES products(id),
    quantity_per_piece DECIMAL(15,4) NOT NULL,
    notes TEXT,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_product_packaging_materials_product ON product_packaging_materials(product_id);
CREATE UNIQUE INDEX idx_product_packaging_materials_unique ON product_packaging_materials(tenant_id, product_id, material_id);
