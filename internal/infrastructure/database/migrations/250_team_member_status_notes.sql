-- Add status and notes columns to construction_project_team for transfer support
ALTER TABLE construction_project_team ADD COLUMN IF NOT EXISTS status VARCHAR(50) DEFAULT 'active';
ALTER TABLE construction_project_team ADD COLUMN IF NOT EXISTS notes TEXT;

-- Sync status from is_active for existing rows
UPDATE construction_project_team SET status = CASE WHEN is_active THEN 'active' ELSE 'inactive' END WHERE status IS NULL;
