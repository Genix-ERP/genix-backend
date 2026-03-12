-- Add WBS and building assignment to team members
ALTER TABLE construction_project_team ADD COLUMN IF NOT EXISTS wbs_id BIGINT REFERENCES construction_wbs(id) ON DELETE SET NULL;
ALTER TABLE construction_project_team ADD COLUMN IF NOT EXISTS building_id BIGINT REFERENCES construction_buildings(id) ON DELETE SET NULL;
