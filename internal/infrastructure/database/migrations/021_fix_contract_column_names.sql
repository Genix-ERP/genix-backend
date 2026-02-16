-- Fix procurement_contracts column names to match handler expectations

-- Rename auto_renew to auto_renewal
ALTER TABLE procurement_contracts RENAME COLUMN auto_renew TO auto_renewal;

-- Rename renewal_notice_days to renewal_term_days
ALTER TABLE procurement_contracts RENAME COLUMN renewal_notice_days TO renewal_term_days;

-- Add missing currency_id column (handler expects it)
ALTER TABLE procurement_contracts ADD COLUMN IF NOT EXISTS currency_id VARCHAR(10);

-- Update currency_id with values from currency column if it exists
UPDATE procurement_contracts SET currency_id = currency WHERE currency_id IS NULL;

-- Update comments
COMMENT ON COLUMN procurement_contracts.auto_renewal IS 'Whether contract auto-renews at expiration';
COMMENT ON COLUMN procurement_contracts.renewal_term_days IS 'Days notice before renewal';
