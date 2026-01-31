-- Add discount_code field to sales_orders table
ALTER TABLE sales_orders ADD COLUMN IF NOT EXISTS discount_code VARCHAR(50);

-- Add index for discount code lookups
CREATE INDEX IF NOT EXISTS idx_sales_orders_discount_code ON sales_orders(discount_code) WHERE discount_code IS NOT NULL;
