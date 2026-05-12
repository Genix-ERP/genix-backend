-- Add labor_rate_type to work_centers: 'monthly' (default) or 'daily'
ALTER TABLE work_centers ADD COLUMN IF NOT EXISTS labor_rate_type VARCHAR(10) DEFAULT 'monthly';
