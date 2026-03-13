-- Link material usage records to sub-stage material assignments
ALTER TABLE construction_material_usage
  ADD COLUMN IF NOT EXISTS sub_stage_material_id BIGINT REFERENCES construction_sub_stage_materials(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_material_usage_sub_stage_material
  ON construction_material_usage(sub_stage_material_id);
