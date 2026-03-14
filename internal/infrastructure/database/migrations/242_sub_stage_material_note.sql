-- Add note field to sub-stage materials for Reja vs Fakt
ALTER TABLE construction_sub_stage_materials
  ADD COLUMN IF NOT EXISTS note TEXT;
