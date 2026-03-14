-- Equipment/Texnika assigned to construction sub-stages (Reja vs Fakt)
CREATE TABLE IF NOT EXISTS construction_sub_stage_equipment (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL,
    sub_stage_id BIGINT NOT NULL REFERENCES construction_sub_stages(id) ON DELETE CASCADE,
    name VARCHAR(500) NOT NULL,
    work_unit VARCHAR(50) NOT NULL DEFAULT 'soat', -- soat, kun, smena
    plan_quantity DECIMAL(18,4) NOT NULL DEFAULT 0,
    fact_quantity DECIMAL(18,4) NOT NULL DEFAULT 0,
    unit_price DECIMAL(18,4) NOT NULL DEFAULT 0,
    note TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sub_stage_equipment_sub_stage ON construction_sub_stage_equipment(sub_stage_id);
CREATE INDEX IF NOT EXISTS idx_sub_stage_equipment_tenant ON construction_sub_stage_equipment(tenant_id);

-- Add plan/fact columns to sub_stage_materials for Reja vs Fakt tracking
ALTER TABLE construction_sub_stage_materials
  ADD COLUMN IF NOT EXISTS plan_quantity DECIMAL(18,4) NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS fact_quantity DECIMAL(18,4) NOT NULL DEFAULT 0;

-- Migrate existing quantity to plan_quantity
UPDATE construction_sub_stage_materials
  SET plan_quantity = quantity
  WHERE plan_quantity = 0 AND quantity > 0;
