-- Add invoice_type to sales_invoices (invoice or credit_note)
ALTER TABLE sales_invoices ADD COLUMN IF NOT EXISTS invoice_type VARCHAR(20) DEFAULT 'invoice';
ALTER TABLE sales_invoices ADD COLUMN IF NOT EXISTS original_invoice_id UUID REFERENCES sales_invoices(id);
ALTER TABLE sales_invoices ADD COLUMN IF NOT EXISTS reason TEXT;

-- Add invoice_type to purchase_invoices (invoice or debit_note)
ALTER TABLE purchase_invoices ADD COLUMN IF NOT EXISTS invoice_type VARCHAR(20) DEFAULT 'invoice';
ALTER TABLE purchase_invoices ADD COLUMN IF NOT EXISTS original_invoice_id UUID REFERENCES purchase_invoices(id);
ALTER TABLE purchase_invoices ADD COLUMN IF NOT EXISTS reason TEXT;

-- Backfill existing rows
UPDATE sales_invoices SET invoice_type = 'invoice' WHERE invoice_type IS NULL;
UPDATE purchase_invoices SET invoice_type = 'invoice' WHERE invoice_type IS NULL;

-- Indexes for filtering by type
CREATE INDEX IF NOT EXISTS idx_sales_invoices_type ON sales_invoices(tenant_id, invoice_type) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_purchase_invoices_type ON purchase_invoices(tenant_id, invoice_type) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_sales_invoices_original ON sales_invoices(original_invoice_id) WHERE original_invoice_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_purchase_invoices_original ON purchase_invoices(original_invoice_id) WHERE original_invoice_id IS NOT NULL;
