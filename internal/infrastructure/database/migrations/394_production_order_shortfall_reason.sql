-- Add shortfall_reason column to production_orders
-- Captures why the produced + scrap quantity ended below the ordered quantity
-- Required when closing a production order with shortfall (enforced in app layer)

ALTER TABLE production_orders
ADD COLUMN IF NOT EXISTS shortfall_reason TEXT;

COMMENT ON COLUMN production_orders.shortfall_reason IS
  'Operator-supplied reason for closing the MO with produced+scrap < quantity_ordered.
   Required by the app when good_quantity + reject_quantity < quantity_ordered.';
