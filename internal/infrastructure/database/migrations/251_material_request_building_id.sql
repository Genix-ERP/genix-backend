-- Add building_id to material requests for stage tracking
ALTER TABLE construction_material_requests
  ADD COLUMN IF NOT EXISTS building_id BIGINT REFERENCES construction_buildings(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_material_requests_building ON construction_material_requests(building_id);
