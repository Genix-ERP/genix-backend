-- Add custom_name column to pipeline_stages for user-edited display names.
-- Default stages use code-based translations; when a user edits the name,
-- the custom_name stores their override so all team members see it.
ALTER TABLE pipeline_stages ADD COLUMN IF NOT EXISTS custom_name VARCHAR(100);
