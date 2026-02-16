-- Migration: Add packaging support to sales order lines
-- This allows selling products in packaged quantities (e.g., "2 cases of 24")

-- Add packaging fields to sales_order_lines
ALTER TABLE sales_order_lines
ADD COLUMN IF NOT EXISTS packaging_id UUID REFERENCES product_packagings(id),
ADD COLUMN IF NOT EXISTS packaging_qty DECIMAL(20, 4);

-- Add comment explaining the fields
COMMENT ON COLUMN sales_order_lines.packaging_id IS 'Reference to product packaging (e.g., 6-pack, case of 24)';
COMMENT ON COLUMN sales_order_lines.packaging_qty IS 'Number of packages ordered (e.g., 2 cases). quantity = packaging_qty * packaging.qty';

-- Create index for packaging lookups
CREATE INDEX IF NOT EXISTS idx_sales_order_lines_packaging ON sales_order_lines(packaging_id);

-- Also add to sales_invoice_lines for consistency
ALTER TABLE sales_invoice_lines
ADD COLUMN IF NOT EXISTS packaging_id UUID REFERENCES product_packagings(id),
ADD COLUMN IF NOT EXISTS packaging_qty DECIMAL(20, 4);

COMMENT ON COLUMN sales_invoice_lines.packaging_id IS 'Reference to product packaging';
COMMENT ON COLUMN sales_invoice_lines.packaging_qty IS 'Number of packages';

CREATE INDEX IF NOT EXISTS idx_sales_invoice_lines_packaging ON sales_invoice_lines(packaging_id);
