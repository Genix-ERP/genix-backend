-- Migration: Add carrier column to sales_orders table

-- Add carrier column if it doesn't exist
ALTER TABLE sales_orders ADD COLUMN IF NOT EXISTS carrier VARCHAR(255);

-- Add shipping_cost column if it doesn't exist (used by frontend)
ALTER TABLE sales_orders ADD COLUMN IF NOT EXISTS shipping_cost DECIMAL(20, 4) DEFAULT 0;

-- Add delivery_date column if it doesn't exist
ALTER TABLE sales_orders ADD COLUMN IF NOT EXISTS delivery_date DATE;
