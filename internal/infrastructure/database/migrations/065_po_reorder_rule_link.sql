-- Migration: Add reorder_rule_id reference to Purchase Order Lines for auto-PO traceability
-- This enables tracking which PO line was auto-generated from which reorder rule
-- Stored at line level since one PO can have multiple items from different reorder rules

-- Add reorder_rule_id column to purchase_order_lines table
ALTER TABLE purchase_order_lines
ADD COLUMN IF NOT EXISTS reorder_rule_id UUID REFERENCES reorder_rules(id) ON DELETE SET NULL;

-- Add index for efficient lookup of PO lines by reorder rule
CREATE INDEX IF NOT EXISTS idx_purchase_order_lines_reorder_rule_id ON purchase_order_lines(reorder_rule_id) WHERE reorder_rule_id IS NOT NULL;

-- Add comment for documentation
COMMENT ON COLUMN purchase_order_lines.reorder_rule_id IS 'Reference to the reorder rule that triggered auto-generation of this PO line (if any)';

-- Also add a flag to purchase_orders to indicate if it was auto-generated from replenishment
ALTER TABLE purchase_orders
ADD COLUMN IF NOT EXISTS is_auto_replenishment BOOLEAN DEFAULT FALSE;

COMMENT ON COLUMN purchase_orders.is_auto_replenishment IS 'True if this PO was auto-generated from reorder rules replenishment';
