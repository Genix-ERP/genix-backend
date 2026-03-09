-- Change product uniqueness from code to barcode and SKU (global ERP best practice)
-- Barcode (EAN/UPC) is the global standard for product identification
-- SKU is the company-internal unique identifier

-- Drop old code uniqueness constraint
ALTER TABLE products DROP CONSTRAINT IF EXISTS products_tenant_code_unique;
ALTER TABLE products DROP CONSTRAINT IF EXISTS products_tenant_org_code_key;

-- Add unique partial indexes for barcode and SKU (only when not null/empty)
CREATE UNIQUE INDEX IF NOT EXISTS idx_products_tenant_barcode_unique
  ON products (tenant_id, barcode)
  WHERE deleted_at IS NULL AND barcode IS NOT NULL AND barcode != '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_products_tenant_sku_unique
  ON products (tenant_id, sku)
  WHERE deleted_at IS NULL AND sku IS NOT NULL AND sku != '';
