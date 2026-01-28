-- Add code columns to fiscal year tables

-- Add code column to fiscal_years if it doesn't exist
ALTER TABLE fiscal_years ADD COLUMN IF NOT EXISTS code VARCHAR(20);

-- Add code column to fiscal_periods if it doesn't exist
ALTER TABLE fiscal_periods ADD COLUMN IF NOT EXISTS code VARCHAR(20) NOT NULL DEFAULT '';
