-- Add resource_type column to distinguish equipment from labor (employee)
ALTER TABLE construction_sub_stage_equipment
  ADD COLUMN IF NOT EXISTS resource_type VARCHAR(20) NOT NULL DEFAULT 'equipment';

CREATE INDEX IF NOT EXISTS idx_sub_stage_equipment_type
  ON construction_sub_stage_equipment(sub_stage_id, resource_type);
