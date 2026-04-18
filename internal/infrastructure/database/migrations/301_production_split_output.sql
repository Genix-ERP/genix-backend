-- Production order split output: bulk production → multiple packaged products
-- Example: 2000 kg cement → 20 bags × 25 kg + 17 bags × 35 kg

ALTER TABLE production_orders
    ADD COLUMN IF NOT EXISTS has_split_output BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS production_split_outputs (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    production_order_id UUID NOT NULL REFERENCES production_orders(id) ON DELETE CASCADE,
    product_id       UUID NOT NULL REFERENCES products(id),
    product_name     VARCHAR(255),
    quantity         DECIMAL(15,4) NOT NULL,          -- dona (pieces)
    unit_weight_kg   DECIMAL(15,4) NOT NULL DEFAULT 1, -- kg per piece
    total_weight_kg  DECIMAL(15,4) NOT NULL DEFAULT 0, -- quantity * unit_weight_kg
    cost_per_kg      DECIMAL(15,4) NOT NULL DEFAULT 0,
    unit_cost        DECIMAL(15,4) NOT NULL DEFAULT 0, -- unit_weight_kg * cost_per_kg
    total_cost       DECIMAL(15,4) NOT NULL DEFAULT 0, -- quantity * unit_cost
    warehouse_id     UUID REFERENCES warehouses(id),
    created_by       UUID REFERENCES users(id),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pso_tenant    ON production_split_outputs(tenant_id);
CREATE INDEX IF NOT EXISTS idx_pso_po        ON production_split_outputs(production_order_id);
CREATE INDEX IF NOT EXISTS idx_pso_product   ON production_split_outputs(product_id);
