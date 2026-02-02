-- Migration: 059_purchase_order_line_packaging.sql
-- Description: Add packaging fields to purchase order lines
-- Date: 2026-02-02

-- Add packaging_id column to purchase_order_lines
ALTER TABLE purchase_order_lines ADD COLUMN IF NOT EXISTS packaging_id UUID REFERENCES product_packagings(id);

-- Add packaging_qty column to purchase_order_lines
ALTER TABLE purchase_order_lines ADD COLUMN IF NOT EXISTS packaging_qty DECIMAL(20, 4);

-- Create index for packaging lookups
CREATE INDEX IF NOT EXISTS idx_purchase_order_lines_packaging_id ON purchase_order_lines(packaging_id);

-- Add comment explaining the packaging fields
COMMENT ON COLUMN purchase_order_lines.packaging_id IS 'Reference to product packaging (e.g., case of 24, pallet)';
COMMENT ON COLUMN purchase_order_lines.packaging_qty IS 'Number of packaging units ordered (e.g., 5 cases)';
