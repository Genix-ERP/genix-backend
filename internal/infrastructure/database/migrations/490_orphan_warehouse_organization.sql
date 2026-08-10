-- Warehouses with no organization hold stock nobody can see.
--
-- Every read path filters on the WAREHOUSE's organization, not on
-- inventory.organization_id:
--
--   ListInventory    AND w.organization_id = $orgID
--   ListWarehouses   AND w.organization_id = $orgID
--   Products.jsx     accessibleWarehouseIds (the same rule again, client-side)
--
-- `w.organization_id = $orgID` is never true when the column is NULL, so a
-- warehouse without an organization is invisible to EVERY organization. Goods
-- received into it are real, posted to the ledger, and unreachable: the
-- operation reads "Bajarildi 200 Dona" and the product reads "Qoldiq 0".
--
-- NUMBERED 490, NOT 489, DELIBERATELY.
-- Version 489 is recorded in schema_migrations on the production database — it
-- was applied before that migration was withdrawn from the repository. Because
-- the runner keys on the integer version and skips versions already recorded, a
-- file numbered 489 would never execute there. It would not fail; it would be
-- silently ignored, which is worse.
--
-- WHAT THIS DOES AND DOES NOT REPAIR
-- Only tenants with exactly ONE organization are backfilled, because only there
-- is the answer unambiguous. A tenant running several companies could have
-- meant any of them, and quietly moving a warehouse — and with it all of its
-- stock and history — into the wrong company is a far more expensive mistake
-- than leaving it visibly broken. Those are reported at the end instead.
--
-- No NOT NULL constraint is added for the same reason: it would fail for
-- exactly the multi-organization tenants this cannot repair.

DO $$
DECLARE
    fixed_count   INTEGER := 0;
    orphan_count  INTEGER := 0;
    r             RECORD;
BEGIN
    -- Backfill from the tenant's sole organization.
    -- (array_agg(id))[1] rather than MIN(id): Postgres has no MIN() for uuid.
    -- HAVING COUNT(*) = 1 means the array holds exactly one element anyway.
    WITH single_org AS (
        SELECT tenant_id, (array_agg(id))[1] AS org_id
        FROM organizations
        GROUP BY tenant_id
        HAVING COUNT(*) = 1
    )
    UPDATE warehouses w
    SET organization_id = s.org_id,
        updated_at      = CURRENT_TIMESTAMP
    FROM single_org s
    WHERE w.organization_id IS NULL
      AND w.tenant_id = s.tenant_id
      AND w.deleted_at IS NULL;

    GET DIAGNOSTICS fixed_count = ROW_COUNT;
    IF fixed_count > 0 THEN
        RAISE NOTICE 'Backfilled organization_id on % warehouse(s); their stock is now visible.', fixed_count;
    END IF;

    -- Report, do not touch, the ones that need a human decision.
    SELECT COUNT(*) INTO orphan_count
    FROM warehouses w
    WHERE w.organization_id IS NULL AND w.deleted_at IS NULL;

    IF orphan_count > 0 THEN
        RAISE WARNING 'Still % warehouse(s) with no organization (multi-organization tenants). Stock in them stays invisible until an organization is assigned:', orphan_count;
        FOR r IN
            SELECT w.id, w.name, w.tenant_id,
                   COALESCE((SELECT SUM(i.quantity_on_hand)
                             FROM inventory i
                             WHERE i.warehouse_id = w.id), 0) AS qty
            FROM warehouses w
            WHERE w.organization_id IS NULL AND w.deleted_at IS NULL
            ORDER BY qty DESC
        LOOP
            RAISE WARNING '  warehouse % (%) tenant % — % unit(s) of stock unreachable', r.name, r.id, r.tenant_id, r.qty;
        END LOOP;
    END IF;
END $$;

-- Finding these again without reading this file.
CREATE INDEX IF NOT EXISTS idx_warehouses_missing_organization
    ON warehouses (tenant_id)
    WHERE organization_id IS NULL AND deleted_at IS NULL;
