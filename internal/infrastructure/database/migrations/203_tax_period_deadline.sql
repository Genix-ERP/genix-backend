-- Add deadline column for tax period payment due dates
ALTER TABLE tax_report_periods ADD COLUMN IF NOT EXISTS deadline DATE;
