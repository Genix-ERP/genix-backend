-- Add start_date, end_date, warning_threshold to budgets table
ALTER TABLE budgets ADD COLUMN IF NOT EXISTS start_date DATE;
ALTER TABLE budgets ADD COLUMN IF NOT EXISTS end_date DATE;
ALTER TABLE budgets ADD COLUMN IF NOT EXISTS warning_threshold DECIMAL(5,2) DEFAULT 80.00;

-- Backfill start_date/end_date from fiscal_years
UPDATE budgets b SET
    start_date = fy.start_date,
    end_date = fy.end_date
FROM fiscal_years fy
WHERE b.fiscal_year_id = fy.id AND b.start_date IS NULL;
