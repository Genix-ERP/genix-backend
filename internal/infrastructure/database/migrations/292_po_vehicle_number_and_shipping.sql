-- Add vehicle_number and requires_shipping to purchase_orders
ALTER TABLE purchase_orders ADD COLUMN IF NOT EXISTS vehicle_number VARCHAR(50);
ALTER TABLE purchase_orders ADD COLUMN IF NOT EXISTS requires_shipping BOOLEAN NOT NULL DEFAULT true;
