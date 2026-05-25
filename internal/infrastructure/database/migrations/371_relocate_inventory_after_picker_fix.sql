-- 371_relocate_inventory_after_picker_fix.sql
--
-- Re-runs migration 368's relocation pass to catch inventory rows
-- that were created AFTER 368 ran with the old project-org-only
-- warehouse picker. The "inventory product" case the user reported
-- ("i created new product and confirmed stage completion but
-- inventory not changed") is exactly this: a Yangi Mahsulot product
-- linked only to the user's active org got its inventory landed in
-- a different-org warehouse (the project's default or fallback),
-- which Inventory.jsx then filters out via accessibleWarehouseIds.
--
-- The runtime fix in this commit adds the user's active org as an
-- earlier warehouse-picker candidate AND auto-links the product to
-- the picked warehouse's org via product_organization_settings, so
-- new operations land in a visible spot. This migration cleans up
-- rows already in the wrong place by:
--   1. Relocating the inventory row to the project's preferred
--      warehouse (project default → oldest active in project's org).
--   2. Linking the product to the warehouse's org so the inventory
--      list won't filter the row out even when the user views as
--      a different active company than the project's org.
--
-- Idempotent: only touches rows whose source warehouse's org IS
-- DISTINCT FROM the project's org. After relocation, src.org ==
-- target.org, so a re-run finds nothing to move.

DO $$
DECLARE
    src_row RECORD;
    target_wh UUID;
    target_org UUID;
    existing_id UUID;
    existing_qty NUMERIC;
    existing_reserved NUMERIC;
BEGIN
    FOR src_row IN
        SELECT
            inv.id            AS inventory_id,
            inv.tenant_id,
            inv.product_id,
            inv.warehouse_id  AS src_warehouse_id,
            inv.quantity_on_hand,
            inv.quantity_reserved,
            cp.id             AS project_id,
            cp.organization_id AS project_org_id,
            cp.warehouse_id   AS project_default_wh_id
        FROM inventory inv
        JOIN warehouses src_w
          ON src_w.id = inv.warehouse_id
        JOIN construction_estimate_line c
          ON UPPER(c.name) = UPPER((SELECT name FROM products p WHERE p.id = inv.product_id))
         AND c.tenant_id   = inv.tenant_id
         AND COALESCE(c.resource_type, '') <> ''
        JOIN construction_estimate_line par
          ON par.id = c.parent_line_id AND par.tenant_id = c.tenant_id
         AND COALESCE(par.approval_status, '') = 'confirmed_engineer'
        JOIN construction_estimate e
          ON e.id = c.estimate_id AND e.tenant_id = c.tenant_id
        JOIN construction_projects cp
          ON cp.id = e.project_id AND cp.deleted_at IS NULL
        WHERE cp.organization_id IS NOT NULL
          AND src_w.organization_id IS DISTINCT FROM cp.organization_id
        GROUP BY inv.id, inv.tenant_id, inv.product_id, inv.warehouse_id,
                 inv.quantity_on_hand, inv.quantity_reserved,
                 cp.id, cp.organization_id, cp.warehouse_id
    LOOP
        target_wh  := NULL;
        target_org := src_row.project_org_id;

        -- Step 1 — project's explicit default. The project create form
        -- already validates that the chosen warehouse belongs to a
        -- usable org; trust it.
        IF src_row.project_default_wh_id IS NOT NULL THEN
            target_wh := src_row.project_default_wh_id;
        END IF;

        -- Step 2 — oldest active warehouse owned by the project's org.
        IF target_wh IS NULL THEN
            SELECT id INTO target_wh
            FROM warehouses
            WHERE tenant_id = src_row.tenant_id
              AND organization_id = src_row.project_org_id
              AND COALESCE(is_active, TRUE) = TRUE
            ORDER BY created_at ASC
            LIMIT 1;
        END IF;

        -- No candidate — leave the row alone, log nothing (the runtime
        -- fix will sort future rows; this one needs human review).
        IF target_wh IS NULL OR target_wh = src_row.src_warehouse_id THEN
            CONTINUE;
        END IF;

        -- Does the target already have an inventory row for this
        -- product? If so, sum the source quantities into it and drop
        -- the source row. Otherwise just rebind the source row's
        -- warehouse_id in place (cheaper than delete+insert).
        SELECT id, quantity_on_hand, COALESCE(quantity_reserved, 0)
          INTO existing_id, existing_qty, existing_reserved
        FROM inventory
        WHERE tenant_id    = src_row.tenant_id
          AND product_id   = src_row.product_id
          AND warehouse_id = target_wh
        LIMIT 1;

        IF existing_id IS NOT NULL THEN
            UPDATE inventory
               SET quantity_on_hand  = COALESCE(existing_qty, 0)
                                     + COALESCE(src_row.quantity_on_hand, 0),
                   quantity_reserved = existing_reserved
                                     + COALESCE(src_row.quantity_reserved, 0),
                   updated_at        = NOW()
             WHERE id = existing_id;
            DELETE FROM inventory WHERE id = src_row.inventory_id;
        ELSE
            UPDATE inventory
               SET warehouse_id = target_wh,
                   updated_at   = NOW()
             WHERE id = src_row.inventory_id;
        END IF;

        -- Belt-and-suspenders: link the product to the target
        -- warehouse's org so even if the user's active company
        -- differs from the project's org later, the inventory row
        -- still surfaces (the products tab INNER JOINs
        -- product_organization_settings).
        INSERT INTO product_organization_settings (
            tenant_id, product_id, organization_id,
            cost_price, list_price, min_price,
            min_stock_level, reorder_point, reorder_quantity
        )
        SELECT
            src_row.tenant_id, src_row.product_id, target_org,
            COALESCE((SELECT cost_price FROM products WHERE id = src_row.product_id), 0),
            COALESCE((SELECT list_price FROM products WHERE id = src_row.product_id), 0),
            0, 0, 0, 0
        WHERE target_org IS NOT NULL
        ON CONFLICT (product_id, organization_id) DO NOTHING;

    END LOOP;
END $$;

-- Second pass — for products consumed in YAKUNIY works whose
-- inventory row is already in the project's org but whose
-- product_organization_settings is NOT linked to that org (because
-- the product was created via Yangi Mahsulot under a DIFFERENT
-- active company), add the link so the row is visible. This is the
-- exact "inventory product" case: product linked to active company
-- "1212121212" only, inventory placed in project's org warehouse,
-- so user viewing as 1212121212 sees the warehouse but the product
-- gets filtered out by the products-tab join because the product
-- isn't linked to 1212121212's org for that warehouse.
INSERT INTO product_organization_settings (
    tenant_id, product_id, organization_id,
    cost_price, list_price, min_price,
    min_stock_level, reorder_point, reorder_quantity
)
SELECT DISTINCT
    inv.tenant_id,
    inv.product_id,
    w.organization_id,
    COALESCE(pr.cost_price, 0),
    COALESCE(pr.list_price, 0),
    0, 0, 0, 0
FROM inventory inv
JOIN warehouses w ON w.id = inv.warehouse_id
JOIN products   pr ON pr.id = inv.product_id AND pr.tenant_id = inv.tenant_id
WHERE w.organization_id IS NOT NULL
  AND pr.deleted_at IS NULL
  AND EXISTS (
      -- Product is referenced by some YAKUNIY consumption — limits
      -- the link-add to construction-relevant rows so we don't
      -- accidentally widen exposure of unrelated products.
      SELECT 1
      FROM construction_estimate_line c
      JOIN construction_estimate_line par
        ON par.id = c.parent_line_id AND par.tenant_id = c.tenant_id
      WHERE UPPER(c.name) = UPPER(pr.name)
        AND c.tenant_id   = inv.tenant_id
        AND COALESCE(par.approval_status, '') = 'confirmed_engineer'
        AND COALESCE(c.resource_type, '') <> ''
  )
ON CONFLICT (product_id, organization_id) DO NOTHING;
