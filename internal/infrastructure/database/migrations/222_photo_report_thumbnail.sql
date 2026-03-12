-- Add thumbnail column to photo reports
ALTER TABLE construction_photo_reports ADD COLUMN IF NOT EXISTS thumbnail TEXT;
