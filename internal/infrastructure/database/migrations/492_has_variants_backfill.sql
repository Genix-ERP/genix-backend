-- products.has_variants was added by 053_product_variants.sql but never
-- backfilled, and nothing cleared it when a variant was deleted. So the flag
-- disagreed with product_variants in both directions:
--   * false where variants exist  -> the Variants tab hid the product
--   * true where none are left    -> AdjustInventory (inventory.go) keeps
--     demanding a variant_id for a product that has no variant to pick,
--     which can only be undone by editing the database by hand.
-- Recompute it from the table once; the handlers keep it in step from here.
UPDATE products p
SET has_variants = EXISTS (
    SELECT 1 FROM product_variants pv
    WHERE pv.product_id = p.id AND pv.deleted_at IS NULL
)
WHERE p.deleted_at IS NULL
  AND p.has_variants IS DISTINCT FROM EXISTS (
    SELECT 1 FROM product_variants pv
    WHERE pv.product_id = p.id AND pv.deleted_at IS NULL
  );

-- Supports both the recompute above and the new
-- `GET /products?has_variants=true` EXISTS filter.
CREATE INDEX IF NOT EXISTS idx_product_variants_product_alive
    ON product_variants (product_id) WHERE deleted_at IS NULL;
