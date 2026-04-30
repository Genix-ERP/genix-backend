-- 370_backfill_yakuniy_zero_price_resources.sql
--
-- Catches the "test 11" case the user reported: an ad-hoc resource
-- grafted onto a YAKUNIY-confirmed work was supposed to fire
-- `processYakuniyAdHocResource`, but the runtime trigger for that
-- function gated on `req.UnitPrice > 0`. Resources added without a
-- unit price slipped through silently — no expense written, no
-- inventory decrement — even though the foreman's done_quantity
-- implied a real consumption.
--
-- Migrations 366 + 367 ran the per-resource expense + inventory
-- backfill earlier, but 366 STEP 1 also had a `c.unit_rate > 0`
-- filter, so the same rows missed those passes too. This migration
-- re-runs the same logic WITHOUT the price filter.
--
-- Idempotency:
--   • STEP 1 INSERTs expenses only for (parent, resource) pairs that
--     don't already have a 'Yakunlangan ish #N — <name>' expense row,
--     so re-runs don't write duplicates.
--   • STEP 2 decrements inventory by the quantity of expenses written
--     by THIS migration's STEP 1 ONLY — keyed via a staging table so
--     the runtime path's separate decrements aren't double-counted.

-- Staging table for the in-flight backfill. Created/cleared at the
-- start so a previous failed run leaves nothing behind, and dropped
-- at the end of this migration. Not a TEMP table because the
-- migration runner may run each statement in its own session.
CREATE TABLE IF NOT EXISTS _backfill_370_inserted (
    expense_id    UUID,
    tenant_id     UUID,
    project_id    BIGINT,
    resource_name TEXT,
    consumed      NUMERIC,
    unit_rate     NUMERIC
);
TRUNCATE TABLE _backfill_370_inserted;

-- ──────────────────────────────────────────────────────────────────
-- STEP 1 — write expenses for missing YAKUNIY resources, no price gate
-- ──────────────────────────────────────────────────────────────────
WITH yak_resources AS (
    SELECT
        c.id                                      AS resource_line_id,
        c.tenant_id,
        c.name                                    AS resource_name,
        COALESCE(c.uom, '')                       AS uom,
        COALESCE(c.norm_rate, 0)                  AS norm_rate,
        COALESCE(c.unit_rate, 0)                  AS unit_rate,
        COALESCE(c.quantity, 0)                   AS own_qty,
        COALESCE(c.quantity_override, FALSE)      AS qty_override,
        p.id                                      AS parent_line_id,
        p.name                                    AS parent_name,
        COALESCE(p.done_quantity, 0)              AS parent_done,
        COALESCE(p.parent_item_number, '')        AS section_path,
        COALESCE(p.confirmed_engineer_at, NOW())  AS confirmed_at,
        e.project_id,
        e.building_id,
        cp.organization_id
    FROM construction_estimate_line c
    JOIN construction_estimate_line p
      ON p.id = c.parent_line_id AND p.tenant_id = c.tenant_id
    JOIN construction_estimate e
      ON e.id = c.estimate_id AND e.tenant_id = c.tenant_id
    JOIN construction_projects cp
      ON cp.id = e.project_id
    WHERE COALESCE(p.approval_status, '') = 'confirmed_engineer'
      AND COALESCE(p.done_quantity, 0) > 0
      AND COALESCE(c.resource_type, '') <> ''
    -- NO unit_rate filter — that's the whole point of this migration.
),
priced AS (
    SELECT
        yr.*,
        CASE
            WHEN qty_override AND own_qty > 0 THEN own_qty
            WHEN norm_rate > 0                THEN parent_done * norm_rate
            ELSE 0
        END AS consumed
    FROM yak_resources yr
),
inserted AS (
    INSERT INTO construction_expense_lines (
        id, tenant_id, organization_id, project_id, stage_id,
        expense_date, description,
        quantity, uom, unit_price,
        amount, currency_code,
        status, approved_by, approved_at,
        created_by, created_at, updated_at
    )
    SELECT
        gen_random_uuid(),
        p.tenant_id,
        p.organization_id,
        p.project_id,
        COALESCE(
            (SELECT s.id FROM construction_stages s
             WHERE s.tenant_id  = p.tenant_id
               AND s.project_id = p.project_id
               AND s.name       = p.section_path
               AND p.building_id IS NOT NULL
               AND s.building_id = p.building_id
             ORDER BY s.id ASC LIMIT 1),
            (SELECT s.id FROM construction_stages s
             WHERE s.tenant_id  = p.tenant_id
               AND s.project_id = p.project_id
               AND s.name       = p.section_path
             ORDER BY s.id ASC LIMIT 1)
        ),
        p.confirmed_at::date,
        'Yakunlangan ish #' || p.parent_line_id::text || ' — ' || p.resource_name,
        p.consumed, p.uom, p.unit_rate,
        p.consumed * p.unit_rate, 'UZS',
        'approved',
        NULL,
        p.confirmed_at,
        NULL, p.confirmed_at, NOW()
    FROM priced p
    WHERE p.consumed > 0
      AND NOT EXISTS (
          SELECT 1 FROM construction_expense_lines x
          WHERE x.tenant_id  = p.tenant_id
            AND x.project_id = p.project_id
            AND x.deleted_at IS NULL
            AND x.description = 'Yakunlangan ish #' || p.parent_line_id::text || ' — ' || p.resource_name
      )
    RETURNING id, tenant_id, project_id, description, quantity, unit_price
)
INSERT INTO _backfill_370_inserted (expense_id, tenant_id, project_id, resource_name, consumed, unit_rate)
SELECT
    ins.id,
    ins.tenant_id,
    ins.project_id,
    -- Recover the resource name from the description prefix
    -- 'Yakunlangan ish #<id> — <name>'. SPLIT_PART on ' — ' is robust
    -- against UTF-8 length quirks (the em-dash is multi-byte) and
    -- against resource names that contain '#' or numerals.
    SPLIT_PART(ins.description, ' — ', 2),
    ins.quantity,
    ins.unit_price
FROM inserted ins;

-- ──────────────────────────────────────────────────────────────────
-- STEP 2 — UPSERT inventory by (tenant, product, warehouse), atomic.
--
-- Uses the unique index `idx_inventory_unique_tenant_product_warehouse`
-- (migration 282) so we can do this in a single statement: insert
-- with negative balance when no row exists, otherwise decrement the
-- existing balance by the consumed amount.
--
-- Warehouse selection mirrors the runtime engine:
--   project default → same-org highest-stock → same-org oldest active
--   → tenant-wide oldest active.
-- ──────────────────────────────────────────────────────────────────
INSERT INTO inventory (
    id, tenant_id, product_id, warehouse_id,
    quantity_on_hand, quantity_reserved,
    created_at, updated_at
)
SELECT
    gen_random_uuid(),
    cpw.tenant_id, cpw.product_id, cpw.warehouse_id,
    -cpw.consumed_total, 0,
    NOW(), NOW()
FROM (
    SELECT
        b.tenant_id,
        pr.id AS product_id,
        COALESCE(
            cp.warehouse_id,
            (SELECT inv.warehouse_id
               FROM inventory inv
               JOIN warehouses w ON w.id = inv.warehouse_id
              WHERE inv.product_id = pr.id AND inv.tenant_id = pr.tenant_id
                AND (cp.organization_id IS NULL OR w.organization_id = cp.organization_id)
              ORDER BY inv.quantity_on_hand DESC NULLS LAST
              LIMIT 1),
            (SELECT w.id FROM warehouses w
              WHERE w.tenant_id = pr.tenant_id
                AND (cp.organization_id IS NULL OR w.organization_id = cp.organization_id)
                AND COALESCE(w.is_active, TRUE) = TRUE
              ORDER BY w.created_at LIMIT 1),
            (SELECT w.id FROM warehouses w
              WHERE w.tenant_id = pr.tenant_id
                AND COALESCE(w.is_active, TRUE) = TRUE
              ORDER BY w.created_at LIMIT 1)
        ) AS warehouse_id,
        SUM(b.consumed) AS consumed_total
    FROM _backfill_370_inserted b
    JOIN products pr
      ON UPPER(pr.name) = UPPER(b.resource_name)
     AND pr.tenant_id   = b.tenant_id
     AND pr.deleted_at IS NULL
    JOIN construction_projects cp
      ON cp.id = b.project_id
    GROUP BY b.tenant_id, pr.id, cp.warehouse_id, cp.organization_id
) cpw
WHERE cpw.warehouse_id IS NOT NULL
ON CONFLICT (tenant_id, product_id, warehouse_id) DO UPDATE
SET quantity_on_hand = inventory.quantity_on_hand + EXCLUDED.quantity_on_hand,
    -- EXCLUDED.quantity_on_hand IS the negative consumed_total, so
    -- adding it here decrements the existing balance — exactly what
    -- the runtime UPDATE path does. ON CONFLICT keeps it atomic so
    -- there's no race between SELECT and INSERT/UPDATE.
    updated_at = NOW();

-- Tidy up.
DROP TABLE IF EXISTS _backfill_370_inserted;
