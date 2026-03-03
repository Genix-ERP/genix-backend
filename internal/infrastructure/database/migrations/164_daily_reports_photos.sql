-- Add photos column to construction_daily_reports
ALTER TABLE construction_daily_reports ADD COLUMN IF NOT EXISTS photos JSONB DEFAULT '[]';
