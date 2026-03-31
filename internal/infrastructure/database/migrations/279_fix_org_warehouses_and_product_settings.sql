-- Fix: Ensure every organization has at least one warehouse and product_organization_settings
-- Root cause: CreateOrganization creates journals & chart of accounts but NOT a default warehouse
-- and does NOT backfill product_organization_settings for existing products.
-- The initial "Main Warehouse" was created at tenant level (NULL organization_id) and only
-- assigned to the first org by migration 277.

-- Step 1: Assign organization_id to any warehouses that still have NULL
UPDATE warehouses
SET organization_id = (
    SELECT o.id FROM organizations o
    WHERE o.tenant_id = warehouses.tenant_id
      AND o.deleted_at IS NULL
    ORDER BY o.created_at ASC
    LIMIT 1
),
updated_at = NOW()
WHERE warehouses.organization_id IS NULL
  AND warehouses.deleted_at IS NULL
  AND EXISTS (
    SELECT 1 FROM organizations o
    WHERE o.tenant_id = warehouses.tenant_id
      AND o.deleted_at IS NULL
  );

-- Step 2: Create a default warehouse for every organization that has NO warehouse
DO $$
DECLARE
    org RECORD;
BEGIN
    FOR org IN
        SELECT o.id AS org_id, o.tenant_id, o.code AS org_code
        FROM organizations o
        WHERE o.deleted_at IS NULL
          AND NOT EXISTS (
              SELECT 1 FROM warehouses w
              WHERE w.organization_id = o.id
                AND w.deleted_at IS NULL
          )
    LOOP
        INSERT INTO warehouses (id, tenant_id, organization_id, code, name, is_default, is_active, reception_steps, delivery_steps, created_at, updated_at)
        VALUES (
            gen_random_uuid(), org.tenant_id, org.org_id,
            'WH-' || org.org_code, 'Main Warehouse', true, true, 1, 1, NOW(), NOW()
        );

        -- Create default warehouse locations for the new warehouse
        INSERT INTO warehouse_locations (id, warehouse_id, code, name, type, is_active)
        SELECT gen_random_uuid(), w.id, 'STOCK', 'Stock', 'storage', true
        FROM warehouses w
        WHERE w.organization_id = org.org_id AND w.deleted_at IS NULL
          AND NOT EXISTS (
              SELECT 1 FROM warehouse_locations wl WHERE wl.warehouse_id = w.id AND wl.code = 'STOCK'
          );
    END LOOP;
END $$;

-- Step 3: Backfill product_organization_settings for any (product, organization) pairs that are missing
-- This ensures all existing products are visible in all existing organizations
INSERT INTO product_organization_settings (tenant_id, product_id, organization_id, cost_price, list_price, min_price, min_stock_level, reorder_point, reorder_quantity)
SELECT p.tenant_id, p.id, o.id, p.cost_price, p.list_price, p.min_price, p.min_stock_level, p.reorder_point, p.reorder_quantity
FROM products p
JOIN organizations o ON o.tenant_id = p.tenant_id AND o.deleted_at IS NULL
WHERE p.deleted_at IS NULL
ON CONFLICT (product_id, organization_id) DO NOTHING;
