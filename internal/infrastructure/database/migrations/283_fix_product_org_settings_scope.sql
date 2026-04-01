-- Fix: Remove cross-organization product_organization_settings entries.
-- Migration 279 backfilled product_organization_settings for ALL products in ALL organizations,
-- making the INNER JOIN org filter in ListProducts ineffective.
-- Products should only be visible in the organization that created them (origin_organization_id)
-- unless explicitly assigned to other orgs.

-- Delete product_organization_settings entries where the product's origin_organization_id
-- does NOT match the settings' organization_id (i.e., remove cross-org visibility)
DELETE FROM product_organization_settings pos
USING products p
WHERE pos.product_id = p.id
  AND p.origin_organization_id IS NOT NULL
  AND pos.organization_id != p.origin_organization_id;

-- For products with NULL origin_organization_id, keep only the oldest org's entry
-- (these were created before multi-org support)
WITH ranked AS (
    SELECT pos.product_id, pos.organization_id,
           ROW_NUMBER() OVER (
               PARTITION BY pos.product_id
               ORDER BY o.created_at ASC
           ) AS rn
    FROM product_organization_settings pos
    JOIN products p ON p.id = pos.product_id AND p.origin_organization_id IS NULL
    JOIN organizations o ON o.id = pos.organization_id
)
DELETE FROM product_organization_settings pos
USING ranked r
WHERE pos.product_id = r.product_id
  AND pos.organization_id = r.organization_id
  AND r.rn > 1;

-- Also set origin_organization_id for products that still have it NULL
-- (assign to the org from their remaining product_organization_settings entry)
UPDATE products p
SET origin_organization_id = (
    SELECT pos.organization_id
    FROM product_organization_settings pos
    WHERE pos.product_id = p.id
    LIMIT 1
)
WHERE p.origin_organization_id IS NULL
  AND p.deleted_at IS NULL
  AND EXISTS (
      SELECT 1 FROM product_organization_settings pos WHERE pos.product_id = p.id
  );
