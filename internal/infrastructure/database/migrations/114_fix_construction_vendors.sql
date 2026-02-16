-- =====================================================
-- FIX CONSTRUCTION PROJECT VENDORS TABLE
-- Add missing columns and notes field
-- =====================================================

-- Add start_date and end_date columns if they don't exist
ALTER TABLE construction_project_vendors
ADD COLUMN IF NOT EXISTS start_date DATE,
ADD COLUMN IF NOT EXISTS end_date DATE,
ADD COLUMN IF NOT EXISTS notes TEXT;

-- Add index for date range queries
CREATE INDEX IF NOT EXISTS idx_project_vendors_dates ON construction_project_vendors(start_date, end_date);
