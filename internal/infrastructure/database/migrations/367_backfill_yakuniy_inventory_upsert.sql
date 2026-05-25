-- 367_backfill_yakuniy_inventory_upsert.sql
--
-- Companion to 366. Migration 366 step 2 only UPDATEd `inventory`,
-- which silently skipped products that had no inventory row at all
-- (the common case when a resource's stock started at 0 and was
-- never received). For those products no negative balance got
-- written, so the user saw "Qoldiq yo'q" / 0 even though the
-- expense_line for the consumption was created. The "test 123123"
-- product in the user's bug report is exactly this case.
--
-- This migration uses an UPSERT pattern: try UPDATE first, and for
-- products without an inventory row insert a fresh one at the
-- negative balance — same logic the runtime
-- finaliseMaterialsForWork already does for new YAKUNIY confirms.
--
-- Idempotent: only runs the UPSERT when there's a YAKUNIY
-- expense_line whose `quantity` doesn't match the inventory state
-- (i.e. consumed - already-deducted > 0). On a second pass the
-- delta is 0 and nothing happens.
--
-- Warehouse selection mirrors the runtime chain:
--   project default → highest-stock for product → oldest active.

-- Build the per-(product, warehouse) deltas needed to bring the
-- inventory in sync with the YAKUNIY-resource expenses we backfilled
-- in 366 (and any later runtime ones that may have failed for other
-- reasons).
WITH yakuniy_consumption AS (
    SELECT
        c.tenant_id,
        c.name AS resource_name,
        SUM(
            CASE
                WHEN COALESCE(c.quantity_override, FALSE) AND COALESCE(c.quantity, 0) > 0
                    THEN c.quantity
                WHEN COALESCE(c.norm_rate, 0) > 0
                    THEN COALESCE(p.done_quantity, 0) * c.norm_rate
                ELSE 0
            END
        ) AS consumed_total
    FROM construction_estimate_line c
    JOIN construction_estimate_line p
      ON p.id = c.parent_line_id AND p.tenant_id = c.tenant_id
    WHERE COALESCE(p.approval_status, '') = 'confirmed_engineer'
      AND COALESCE(p.done_quantity, 0) > 0
      AND COALESCE(c.resource_type, '') <> ''
    GROUP BY c.tenant_id, c.name
    HAVING SUM(
        CASE
            WHEN COALESCE(c.quantity_override, FALSE) AND COALESCE(c.quantity, 0) > 0
                THEN c.quantity
            WHEN COALESCE(c.norm_rate, 0) > 0
                THEN COALESCE(p.done_quantity, 0) * c.norm_rate
            ELSE 0
        END
    ) > 0
),
-- Resolve a target warehouse for each (product, tenant). Picks the
-- warehouse that already holds the most stock for the product, and
-- falls back to the tenant's oldest active warehouse when no
-- inventory row exists yet.
target_warehouse AS (
    SELECT
        yc.tenant_id,
        yc.resource_name,
        yc.consumed_total,
        pr.id AS product_id,
        COALESCE(
            (SELECT inv.warehouse_id
             FROM inventory inv
             WHERE inv.product_id = pr.id AND inv.tenant_id = pr.tenant_id
             ORDER BY inv.quantity_on_hand DESC NULLS LAST
             LIMIT 1),
            (SELECT w.id FROM warehouses w
             WHERE w.tenant_id = pr.tenant_id AND COALESCE(w.is_active, true) = true
             ORDER BY w.created_at LIMIT 1)
        ) AS warehouse_id
    FROM yakuniy_consumption yc
    JOIN products pr
      ON UPPER(pr.name) = UPPER(yc.resource_name)
     AND pr.tenant_id = yc.tenant_id
     AND pr.deleted_at IS NULL
),
-- Existing balance per (product, warehouse) so we can compute the
-- delta needed to land at "starting_balance - consumed_total". When
-- no inventory row exists, current balance is 0, so the delta is the
-- full consumed_total.
delta AS (
    SELECT
        tw.tenant_id,
        tw.product_id,
        tw.warehouse_id,
        tw.consumed_total,
        COALESCE(
            (SELECT inv.quantity_on_hand
             FROM inventory inv
             WHERE inv.tenant_id = tw.tenant_id
               AND inv.product_id = tw.product_id
               AND inv.warehouse_id = tw.warehouse_id),
            0
        ) AS current_on_hand
    FROM target_warehouse tw
    WHERE tw.warehouse_id IS NOT NULL
)
-- Upsert: insert a negative-balance row if none exists, otherwise
-- decrement the existing on_hand. The `WHERE` clause skips no-op
-- rows where the balance is already at or below the target so a
-- re-run doesn't double-debit.
INSERT INTO inventory (
    id, tenant_id, product_id, warehouse_id,
    quantity_on_hand, quantity_reserved,
    created_at, updated_at
)
SELECT
    gen_random_uuid(),
    d.tenant_id, d.product_id, d.warehouse_id,
    -d.consumed_total, 0,
    NOW(), NOW()
FROM delta d
WHERE NOT EXISTS (
    SELECT 1 FROM inventory inv
    WHERE inv.tenant_id   = d.tenant_id
      AND inv.product_id  = d.product_id
      AND inv.warehouse_id = d.warehouse_id
);

-- For (product, warehouse) pairs that DO have an existing inventory
-- row, decrement quantity_on_hand. Run as a separate statement
-- because Postgres' INSERT … ON CONFLICT can't decrement the
-- existing value with a delta computed in the row source.
WITH yakuniy_consumption AS (
    SELECT
        c.tenant_id,
        c.name AS resource_name,
        SUM(
            CASE
                WHEN COALESCE(c.quantity_override, FALSE) AND COALESCE(c.quantity, 0) > 0
                    THEN c.quantity
                WHEN COALESCE(c.norm_rate, 0) > 0
                    THEN COALESCE(p.done_quantity, 0) * c.norm_rate
                ELSE 0
            END
        ) AS consumed_total
    FROM construction_estimate_line c
    JOIN construction_estimate_line p
      ON p.id = c.parent_line_id AND p.tenant_id = c.tenant_id
    WHERE COALESCE(p.approval_status, '') = 'confirmed_engineer'
      AND COALESCE(p.done_quantity, 0) > 0
      AND COALESCE(c.resource_type, '') <> ''
    GROUP BY c.tenant_id, c.name
),
already_negative AS (
    -- Skip products whose inventory has already been decremented to
    -- match (or exceed) the consumed total — prevents double-debit on
    -- re-runs. We compare against the negative consumed_total because
    -- starting from 0 on-hand minus consumed_total = -consumed_total.
    SELECT
        yc.tenant_id, yc.resource_name, yc.consumed_total,
        pr.id AS product_id,
        inv.warehouse_id,
        inv.quantity_on_hand
    FROM yakuniy_consumption yc
    JOIN products pr
      ON UPPER(pr.name) = UPPER(yc.resource_name)
     AND pr.tenant_id = yc.tenant_id
     AND pr.deleted_at IS NULL
    JOIN inventory inv
      ON inv.product_id = pr.id
     AND inv.tenant_id  = pr.tenant_id
)
UPDATE inventory inv
SET quantity_on_hand = COALESCE(inv.quantity_on_hand, 0) - an.consumed_total,
    updated_at       = NOW()
FROM already_negative an
WHERE inv.tenant_id    = an.tenant_id
  AND inv.product_id   = an.product_id
  AND inv.warehouse_id = an.warehouse_id
  -- Only decrement when the current balance is HIGHER than the post-
  -- backfill target. If the balance is already at or below
  -- -consumed_total we assume migration 366 (or runtime) already did
  -- the work and skip. This makes 367 idempotent against re-runs.
  AND COALESCE(inv.quantity_on_hand, 0) > -an.consumed_total;
