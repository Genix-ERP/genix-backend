-- Add code column to budgets table
ALTER TABLE budgets ADD COLUMN IF NOT EXISTS code VARCHAR(50);
