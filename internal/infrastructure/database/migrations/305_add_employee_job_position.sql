-- Migration 305: Add job_position_id column to employees table
-- Links employees to job_positions for proper position tracking

ALTER TABLE employees
ADD COLUMN IF NOT EXISTS job_position_id UUID REFERENCES job_positions(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_employees_job_position ON employees(job_position_id);
