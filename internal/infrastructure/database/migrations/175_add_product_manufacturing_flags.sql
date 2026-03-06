-- Add manufacturing-related flags to products table
-- is_manufacturable: whether this product can be manufactured
-- auto_manufacture: whether to auto-create production orders when stock is insufficient

ALTER TABLE products ADD COLUMN IF NOT EXISTS is_manufacturable BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE products ADD COLUMN IF NOT EXISTS auto_manufacture BOOLEAN NOT NULL DEFAULT false;
