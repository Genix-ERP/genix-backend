-- Add price_include column to tax_rates table
-- When true, the unit price in orders/invoices already includes this tax
-- The system will back-calculate: tax = price * rate / (100 + rate)
ALTER TABLE tax_rates ADD COLUMN IF NOT EXISTS price_include BOOLEAN DEFAULT false;
