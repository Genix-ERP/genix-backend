-- Fix missing columns for expenses and payroll modules

-- Fix 1: Add deleted_at column to expenses table if it doesn't exist
ALTER TABLE expenses ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;

-- Fix 2: Add category_name column to expenses table for caching category names
ALTER TABLE expenses ADD COLUMN IF NOT EXISTS category_name VARCHAR(255);

-- Fix 3: Add deleted_at column to payroll_periods table if it doesn't exist
ALTER TABLE payroll_periods ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;

-- Fix 4: Add deleted_at column to payroll_entries table if it doesn't exist
ALTER TABLE payroll_entries ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;

-- Create indexes for soft delete queries
CREATE INDEX IF NOT EXISTS idx_expenses_deleted_at ON expenses(deleted_at) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_payroll_periods_deleted_at ON payroll_periods(deleted_at) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_payroll_entries_deleted_at ON payroll_entries(deleted_at) WHERE deleted_at IS NULL;
