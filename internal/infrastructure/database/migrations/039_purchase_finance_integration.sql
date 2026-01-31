-- Migration: Purchase-Finance Integration
-- Adds columns to link purchase invoices with purchase orders and goods receipts

-- Add purchase_order_id column to purchase_invoices
ALTER TABLE purchase_invoices ADD COLUMN IF NOT EXISTS purchase_order_id UUID REFERENCES purchase_orders(id);

-- Add goods_receipt_id column to purchase_invoices
ALTER TABLE purchase_invoices ADD COLUMN IF NOT EXISTS goods_receipt_id UUID REFERENCES goods_receipts(id);

-- Add three_way_match_status column for validation
ALTER TABLE purchase_invoices ADD COLUMN IF NOT EXISTS three_way_match_status VARCHAR(20) DEFAULT 'pending';
-- Values: pending, matched, mismatch

-- Add paid_amount column if not exists (for partial payment tracking)
ALTER TABLE purchase_invoices ADD COLUMN IF NOT EXISTS paid_amount DECIMAL(20, 4) DEFAULT 0;

-- Create indexes for fast lookup
CREATE INDEX IF NOT EXISTS idx_purchase_invoices_po_id ON purchase_invoices(purchase_order_id);
CREATE INDEX IF NOT EXISTS idx_purchase_invoices_gr_id ON purchase_invoices(goods_receipt_id);

-- Add supplier_id and supplier_name aliases (frontend uses supplier, backend uses vendor)
-- These are nullable since existing records may not have them
ALTER TABLE purchase_invoices ADD COLUMN IF NOT EXISTS supplier_id UUID REFERENCES contacts(id);
ALTER TABLE purchase_invoices ADD COLUMN IF NOT EXISTS supplier_name VARCHAR(255);

-- Copy vendor_id to supplier_id where supplier_id is null
UPDATE purchase_invoices SET supplier_id = vendor_id WHERE supplier_id IS NULL AND vendor_id IS NOT NULL;
