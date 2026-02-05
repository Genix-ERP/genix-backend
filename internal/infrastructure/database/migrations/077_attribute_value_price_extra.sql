-- Migration: Add price_extra to product_attribute_values
-- This allows setting a default price extra when creating attribute values
-- which will be used when adding the value to product configurations

-- Add price_extra column to product_attribute_values
ALTER TABLE product_attribute_values ADD COLUMN IF NOT EXISTS price_extra NUMERIC(12,4) DEFAULT 0;
