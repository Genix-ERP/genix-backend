-- Migration: Add RFQ reference to Purchase Orders for RFQ-to-PO conversion
-- This enables tracking which PO was created from which RFQ

-- Add rfq_id column to purchase_orders table
ALTER TABLE purchase_orders
ADD COLUMN IF NOT EXISTS rfq_id UUID REFERENCES rfqs(id) ON DELETE SET NULL;

-- Add index for efficient lookup of POs by RFQ
CREATE INDEX IF NOT EXISTS idx_purchase_orders_rfq_id ON purchase_orders(rfq_id) WHERE rfq_id IS NOT NULL;

-- Add comment for documentation
COMMENT ON COLUMN purchase_orders.rfq_id IS 'Reference to the RFQ from which this PO was created (if any)';
