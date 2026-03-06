-- Backfill product_organization_settings for existing products
-- This ensures existing products remain visible to all organizations in their tenant
-- by creating a settings entry for each organization

INSERT INTO product_organization_settings (tenant_id, product_id, organization_id, cost_price, list_price, min_price, min_stock_level, reorder_point, reorder_quantity)
SELECT p.tenant_id, p.id, o.id, p.cost_price, p.list_price, p.min_price, p.min_stock_level, p.reorder_point, p.reorder_quantity
FROM products p
JOIN organizations o ON o.tenant_id = p.tenant_id AND o.deleted_at IS NULL
WHERE p.deleted_at IS NULL
ON CONFLICT (product_id, organization_id) DO NOTHING;
