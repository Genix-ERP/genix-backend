-- 368_relocate_yakuniy_inventory_to_project_org.sql
--
-- Re-routes inventory rows that landed in a warehouse owned by a
-- different organization than the project's owning organization.
-- The Inventory.jsx products tab filters out inventory rows whose
-- warehouse.organization_id != activeCompany.id, so a row in the
-- "wrong" warehouse is invisible to the user — they see "Qoldiq
-- yo'q" / 0 even though the database holds the correct deduction.
--
-- Triggered by the bug where migration 367 picked "oldest active
-- tenant warehouse" as the fallback target, which on multi-org
-- tenants happened to belong to a different company than the
-- project. The runtime code in this migration's commit makes the
-- warehouse picker org-aware so this never happens again; the
-- migration cleans up the few rows already in the wrong place.
--
-- Strategy: for every inventory row whose product is referenced by
-- a YAKUNIY-confirmed estimate_line in some project P, and whose
-- warehouse_id belongs to an org different from P's organization_id,
-- relocate it to P's preferred warehouse:
--   1. construction_projects.warehouse_id (explicit pick), OR
--   2. the oldest active warehouse owned by P's org, OR
--   3. (no candidate found) leave the row alone — manual fix needed.
--
-- When a target warehouse already has an inventory row for the same
-- product, the quantities are summed and the source row is deleted.
-- Otherwise the source row is updated in place (cheaper than a
-- delete+insert).

DO $$
DECLARE
    src_row RECORD;
    target_wh UUID;
    target_org UUID;
    existing_id UUID;
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
        -- Limit to products that are referenced as resources by a
        -- YAKUNIY-confirmed parent work somewhere in the project graph.
        -- This avoids touching legitimate inventory that was placed
        -- in a different-org warehouse on purpose (eg cross-company
        -- transfers).
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
        -- Only relocate when the source warehouse is in a DIFFERENT
        -- org than the project. If they match, the row is already
        -- correct.
        WHERE cp.organization_id IS NOT NULL
          AND src_w.organization_id IS DISTINCT FROM cp.organization_id
        GROUP BY inv.id, inv.tenant_id, inv.product_id, inv.warehouse_id,
                 inv.quantity_on_hand, inv.quantity_reserved,
                 cp.id, cp.organization_id, cp.warehouse_id
    LOOP
        target_wh  := NULL;
        target_org := src_row.project_org_id;

        -- Step 1 — project's explicit default. Already org-validated
        -- by the project create form; trust it.
        IF src_row.project_default_wh_id IS NOT NULL THEN
            target_wh := src_row.project_default_wh_id;
        END IF;

        -- Step 2 — oldest active warehouse in the project's org.
        IF target_wh IS NULL THEN
            SELECT id INTO target_wh
            FROM warehouses
            WHERE tenant_id = src_row.tenant_id
              AND organization_id = target_org
              AND COALESCE(is_active, true) = true
            ORDER BY created_at ASC
            LIMIT 1;
        END IF;

        -- No candidate? skip — leave the row alone for manual review.
        IF target_wh IS NULL THEN
            CONTINUE;
        END IF;

        -- Same warehouse already? defensive — should not happen given
        -- our `IS DISTINCT FROM` filter above, but cheap to check.
        IF target_wh = src_row.src_warehouse_id THEN
            CONTINUE;
        END IF;

        -- Does the target warehouse already have a row for this
        -- product? If so, sum into it and delete the source.
        SELECT id INTO existing_id
        FROM inventory
        WHERE tenant_id    = src_row.tenant_id
          AND product_id   = src_row.product_id
          AND warehouse_id = target_wh
        LIMIT 1;

        IF existing_id IS NOT NULL THEN
            UPDATE inventory
            SET quantity_on_hand = COALESCE(quantity_on_hand, 0) + COALESCE(src_row.quantity_on_hand, 0),
                quantity_reserved = COALESCE(quantity_reserved, 0) + COALESCE(src_row.quantity_reserved, 0),
                updated_at = NOW()
            WHERE id = existing_id;

            DELETE FROM inventory WHERE id = src_row.inventory_id;
        ELSE
            -- No conflicting row — re-point the existing one in place.
            UPDATE inventory
            SET warehouse_id = target_wh,
                updated_at   = NOW()
            WHERE id = src_row.inventory_id;
        END IF;
    END LOOP;
END $$;
