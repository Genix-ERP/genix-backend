-- Migration: Add purchase_price column to products table
-- The replenishment feature expects purchase_price column

ALTER TABLE products ADD COLUMN IF NOT EXISTS purchase_price DECIMAL(20, 4) DEFAULT 0;

-- Update purchase_price from cost_price for existing records
UPDATE products SET purchase_price = COALESCE(cost_price, 0) WHERE purchase_price IS NULL OR purchase_price = 0;
