-- Add tax_rate_id to purchase_invoices for proper VAT tracking
ALTER TABLE purchase_invoices ADD COLUMN IF NOT EXISTS tax_rate_id UUID REFERENCES tax_rates(id);

-- Create index for tax rate lookups
CREATE INDEX IF NOT EXISTS idx_purchase_invoices_tax_rate_id ON purchase_invoices(tax_rate_id) WHERE tax_rate_id IS NOT NULL;
