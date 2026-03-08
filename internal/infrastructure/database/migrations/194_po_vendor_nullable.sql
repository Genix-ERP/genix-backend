-- Migration: 194_po_vendor_nullable
-- Description: Allow purchase orders without a vendor (for auto-replenishment)

ALTER TABLE purchase_orders ALTER COLUMN vendor_id DROP NOT NULL;
