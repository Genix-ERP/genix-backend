-- Make products and categories shared across organizations within a tenant.
-- The organization_id column is renamed to origin_organization_id (audit trail only).
-- Unique constraints revert to tenant-level (not org-level).

-- Auto-resolve duplicate product codes across orgs by appending org suffix
DO $$
DECLARE
  rec RECORD;
  suffix INT;
BEGIN
  FOR rec IN
    SELECT p.id, p.tenant_id, p.code, p.organization_id,
           ROW_NUMBER() OVER (PARTITION BY p.tenant_id, p.code ORDER BY p.created_at ASC) as rn
    FROM products p
    WHERE p.deleted_at IS NULL
      AND EXISTS (
        SELECT 1 FROM products p2
        WHERE p2.tenant_id = p.tenant_id AND p2.code = p.code AND p2.id != p.id AND p2.deleted_at IS NULL
      )
  LOOP
    IF rec.rn > 1 THEN
      UPDATE products SET code = rec.code || '-' || rec.rn WHERE id = rec.id;
    END IF;
  END LOOP;
END $$;

-- Auto-resolve duplicate category codes across orgs by appending org suffix
DO $$
DECLARE
  rec RECORD;
BEGIN
  FOR rec IN
    SELECT pc.id, pc.tenant_id, pc.code, pc.organization_id,
           ROW_NUMBER() OVER (PARTITION BY pc.tenant_id, pc.code ORDER BY pc.created_at ASC) as rn
    FROM product_categories pc
    WHERE pc.deleted_at IS NULL
      AND EXISTS (
        SELECT 1 FROM product_categories pc2
        WHERE pc2.tenant_id = pc.tenant_id AND pc2.code = pc.code AND pc2.id != pc.id AND pc2.deleted_at IS NULL
      )
  LOOP
    IF rec.rn > 1 THEN
      UPDATE product_categories SET code = rec.code || '-' || rec.rn WHERE id = rec.id;
    END IF;
  END LOOP;
END $$;

-- Products: update unique constraint to tenant-level
ALTER TABLE products DROP CONSTRAINT IF EXISTS products_tenant_org_code_key;
ALTER TABLE products ADD CONSTRAINT products_tenant_code_unique UNIQUE (tenant_id, code);

-- Rename organization_id to origin_organization_id
ALTER TABLE products RENAME COLUMN organization_id TO origin_organization_id;

-- Categories: update unique constraint to tenant-level
ALTER TABLE product_categories DROP CONSTRAINT IF EXISTS product_categories_tenant_org_code_key;
ALTER TABLE product_categories ADD CONSTRAINT product_categories_tenant_code_unique UNIQUE (tenant_id, code);

-- Rename organization_id to origin_organization_id
ALTER TABLE product_categories RENAME COLUMN organization_id TO origin_organization_id;

COMMENT ON COLUMN products.origin_organization_id IS 'The organization that originally created this product. Products are visible to all orgs in the tenant.';
COMMENT ON COLUMN product_categories.origin_organization_id IS 'The organization that originally created this category. Categories are visible to all orgs in the tenant.';
