-- =====================================================
-- 334_product_search_key.sql
-- Adds a `search_key` column to products — a short, normalised
-- identifier shared across organisations within a tenant so that
-- construction companies (long Cyrillic names, GOST codes) and
-- manufacturing companies (short technical names) can link to the
-- same logical product when creating sales/purchase orders or when
-- creating products from smeta uploads.
--
-- The key is NOT unique per tenant — multiple products from
-- different organisations may intentionally share a key.
-- =====================================================

ALTER TABLE products
    ADD COLUMN IF NOT EXISTS search_key VARCHAR(64);

-- Index for fast lookup by key within a tenant (for order cross-match
-- and smeta same-name copy).
CREATE INDEX IF NOT EXISTS idx_products_tenant_search_key
    ON products(tenant_id, search_key)
    WHERE search_key IS NOT NULL AND deleted_at IS NULL;

-- Helpful for the smeta "find product with this name in another org
-- within the same tenant" lookup.
CREATE INDEX IF NOT EXISTS idx_products_tenant_name_active
    ON products(tenant_id, lower(name))
    WHERE deleted_at IS NULL;
