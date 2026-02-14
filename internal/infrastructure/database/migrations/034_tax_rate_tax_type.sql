-- Add tax_type column to tax_rates table
-- tax_type indicates whether this is a sales tax or purchase tax
ALTER TABLE tax_rates ADD COLUMN IF NOT EXISTS tax_type VARCHAR(20) DEFAULT 'sales';

-- Add check constraint for valid tax_type values
ALTER TABLE tax_rates DROP CONSTRAINT IF EXISTS tax_rates_tax_type_check;
ALTER TABLE tax_rates ADD CONSTRAINT tax_rates_tax_type_check CHECK (tax_type IN ('sales', 'purchase'));

-- Create index for filtering by tax_type
CREATE INDEX IF NOT EXISTS idx_tax_rates_tax_type ON tax_rates(tenant_id, tax_type);
