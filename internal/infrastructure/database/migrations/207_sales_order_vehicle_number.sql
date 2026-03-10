-- Add vehicle_number to sales_orders for tracking transport/truck license plate
ALTER TABLE sales_orders ADD COLUMN IF NOT EXISTS vehicle_number VARCHAR(50);
