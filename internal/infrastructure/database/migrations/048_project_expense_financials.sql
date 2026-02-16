-- Add vendor_id and purchase_invoice_id to project_expenses for financials integration
-- When a project expense is created, it creates a corresponding purchase invoice (payable)
-- When the purchase invoice is paid, the project expense status is updated to 'paid'

-- Add vendor_id column
ALTER TABLE project_expenses ADD COLUMN IF NOT EXISTS vendor_id UUID REFERENCES contacts(id) ON DELETE SET NULL;

-- Add purchase_invoice_id to link expense to the payable
ALTER TABLE project_expenses ADD COLUMN IF NOT EXISTS purchase_invoice_id UUID REFERENCES purchase_invoices(id) ON DELETE SET NULL;

-- Create index for vendor_id
CREATE INDEX IF NOT EXISTS idx_project_expenses_vendor ON project_expenses(vendor_id);

-- Create index for purchase_invoice_id
CREATE INDEX IF NOT EXISTS idx_project_expenses_invoice ON project_expenses(purchase_invoice_id);

-- Update status enum to include 'paid'
-- Status values: pending, approved, rejected, paid
