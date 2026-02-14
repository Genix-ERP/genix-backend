-- Migration: Add module visibility fields to products (Odoo-style)
-- This allows configuring which modules a product is visible in

-- Add module visibility columns to products table
ALTER TABLE products
ADD COLUMN IF NOT EXISTS can_be_sold BOOLEAN DEFAULT true,
ADD COLUMN IF NOT EXISTS can_be_purchased BOOLEAN DEFAULT true,
ADD COLUMN IF NOT EXISTS available_in_pos BOOLEAN DEFAULT false,
ADD COLUMN IF NOT EXISTS can_be_expensed BOOLEAN DEFAULT false,
ADD COLUMN IF NOT EXISTS can_be_rented BOOLEAN DEFAULT false,
ADD COLUMN IF NOT EXISTS can_be_subcontracted BOOLEAN DEFAULT false,
ADD COLUMN IF NOT EXISTS is_overhead_expense BOOLEAN DEFAULT false;

-- Add comments explaining the module visibility options
COMMENT ON COLUMN products.can_be_sold IS 'Product can be sold (visible in Sales module)';
COMMENT ON COLUMN products.can_be_purchased IS 'Product can be purchased (visible in Purchase module)';
COMMENT ON COLUMN products.available_in_pos IS 'Product available in Point of Sale';
COMMENT ON COLUMN products.can_be_expensed IS 'Product can be used in Expenses module';
COMMENT ON COLUMN products.can_be_rented IS 'Product can be rented (visible in Rental module)';
COMMENT ON COLUMN products.can_be_subcontracted IS 'Product can be subcontracted (Manufacturing)';

-- Update existing products to have sensible defaults based on is_sellable and is_purchasable
UPDATE products SET
    can_be_sold = is_sellable,
    can_be_purchased = is_purchasable
WHERE can_be_sold IS NULL OR can_be_purchased IS NULL;

-- Create index for commonly filtered columns
CREATE INDEX IF NOT EXISTS idx_products_can_be_sold ON products(can_be_sold) WHERE can_be_sold = true;
CREATE INDEX IF NOT EXISTS idx_products_can_be_purchased ON products(can_be_purchased) WHERE can_be_purchased = true;
CREATE INDEX IF NOT EXISTS idx_products_available_in_pos ON products(available_in_pos) WHERE available_in_pos = true;
