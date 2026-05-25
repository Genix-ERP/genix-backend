-- 372_backfill_approved_reservations_inventory.sql
--
-- Repairs the "Test inventory 123" state: material_reservations
-- rows with status='approved' but no corresponding inventory
-- decrement. This happens when the previous non-transactional
-- finaliseMaterialsForWork marked the reservation approved and then
-- the follow-up inventory UPDATE/INSERT silently no-op'd (UPDATE
-- matched 0 rows AND the INSERT failed for any reason, or didn't
-- run at all because of a partial failure). The runtime fix in
-- this commit wraps both writes in a single transaction with an
-- atomic UPSERT so this can't happen for new approvals.
--
-- This migration plugs the gap for already-approved reservations
-- whose inventory state hasn't been corrected. Strategy:
--
-- For each (tenant_id, product_id, warehouse_id) where the SUM of
-- approved-reservation quantities exceeds the (negative of the)
-- existing inventory_on_hand, decrement the inventory by the gap.
-- This guarantees:
--   • Products with no inventory row at all (the user's case) get
--     a row at -SUM(approved.qty).
--   • Products whose inventory was partially deducted catch up to
--     the full approved-reservation total.
--   • Products whose inventory was fully deducted already (the
--     happy path) are skipped because the gap is <= 0.
--
-- Idempotency: the gap math means a re-run after this migration
-- finds gap = 0 for every (product, warehouse) that's already
-- in sync, so nothing happens.

WITH approved_totals AS (
    -- Sum the quantity of every approved reservation per
    -- (tenant, product, warehouse). NULL warehouse rows can't be
    -- decremented anywhere, so we drop them — they need manual
    -- review.
    SELECT
        mr.tenant_id,
        mr.product_id,
        mr.warehouse_id,
        SUM(mr.quantity) AS approved_qty
    FROM material_reservations mr
    WHERE mr.status = 'approved'
      AND mr.deleted_at IS NULL
      AND mr.warehouse_id IS NOT NULL
    GROUP BY mr.tenant_id, mr.product_id, mr.warehouse_id
),
existing_inventory AS (
    -- Current inventory level per (tenant, product, warehouse).
    -- LEFT JOINed below so missing rows show up with on_hand=0.
    SELECT
        inv.tenant_id,
        inv.product_id,
        inv.warehouse_id,
        inv.quantity_on_hand
    FROM inventory inv
),
gap AS (
    -- The deduction shortfall: how much MORE we'd need to subtract
    -- from inventory.quantity_on_hand to bring it to (or past) the
    -- "fully-deducted" target of -approved_qty. Positive number =
    -- there's a real gap. Zero or negative = already in sync (or
    -- over-decremented elsewhere — leave alone).
    SELECT
        at.tenant_id,
        at.product_id,
        at.warehouse_id,
        at.approved_qty,
        COALESCE(ei.quantity_on_hand, 0) AS current_on_hand,
        (COALESCE(ei.quantity_on_hand, 0) - (-at.approved_qty)) AS gap_qty
    FROM approved_totals at
    LEFT JOIN existing_inventory ei
      ON ei.tenant_id    = at.tenant_id
     AND ei.product_id   = at.product_id
     AND ei.warehouse_id = at.warehouse_id
    WHERE COALESCE(ei.quantity_on_hand, 0) > -at.approved_qty
)
INSERT INTO inventory (
    id, tenant_id, product_id, warehouse_id,
    quantity_on_hand, quantity_reserved,
    created_at, updated_at
)
SELECT
    gen_random_uuid(),
    g.tenant_id, g.product_id, g.warehouse_id,
    -g.gap_qty, 0,
    NOW(), NOW()
FROM gap g
ON CONFLICT (tenant_id, product_id, warehouse_id) DO UPDATE
SET quantity_on_hand = inventory.quantity_on_hand + EXCLUDED.quantity_on_hand,
    -- EXCLUDED.quantity_on_hand here is -gap_qty (the amount we
    -- want to subtract from the existing balance), so adding it
    -- decrements correctly:
    --   current + (-gap) = current - gap = -approved_qty ✓
    updated_at      = NOW();

-- ──────────────────────────────────────────────────────────────────
-- Companion: write the per-resource expense lines that the runtime
-- skipped for these approved reservations. Without these, the
-- Moliya → Xarajatlar feed and Byudjet Fakt totals stay 0 even
-- though the inventory was just decremented above.
--
-- Description shape mirrors processYakuniyAdHocResource so a future
-- re-run sees the marker and skips. We pull the parent work id and
-- resource name from the matching estimate_line_id on the
-- reservation.
-- ──────────────────────────────────────────────────────────────────
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
    mr.tenant_id,
    cp.organization_id,
    cp.id,
    -- Stage lookup: prefer same-building section, fall back to
    -- name-only match.
    COALESCE(
        (SELECT s.id FROM construction_stages s
         WHERE s.tenant_id  = mr.tenant_id
           AND s.project_id = cp.id
           AND s.name       = COALESCE(par.parent_item_number, '')
           AND e.building_id IS NOT NULL
           AND s.building_id = e.building_id
         ORDER BY s.id ASC LIMIT 1),
        (SELECT s.id FROM construction_stages s
         WHERE s.tenant_id  = mr.tenant_id
           AND s.project_id = cp.id
           AND s.name       = COALESCE(par.parent_item_number, '')
         ORDER BY s.id ASC LIMIT 1)
    ),
    COALESCE(mr.approved_at, mr.updated_at, NOW())::date,
    'Yakunlangan ish #' || par.id::text || ' — ' || pr.name,
    mr.quantity, COALESCE(mr.unit, ''), mr.unit_cost,
    mr.total_cost, 'UZS',
    'approved',
    NULL,
    COALESCE(mr.approved_at, mr.updated_at, NOW()),
    NULL,
    COALESCE(mr.approved_at, mr.updated_at, NOW()),
    NOW()
FROM material_reservations mr
JOIN construction_estimate_line par
  ON par.id = mr.estimate_line_id AND par.tenant_id = mr.tenant_id
JOIN construction_estimate e
  ON e.id = par.estimate_id AND e.tenant_id = par.tenant_id
JOIN construction_projects cp
  ON cp.id = e.project_id AND cp.deleted_at IS NULL
JOIN products pr
  ON pr.id = mr.product_id AND pr.tenant_id = mr.tenant_id
WHERE mr.status = 'approved'
  AND mr.deleted_at IS NULL
  AND mr.quantity > 0
  AND NOT EXISTS (
      -- Skip if the per-resource expense already exists.
      SELECT 1
      FROM construction_expense_lines x
      WHERE x.tenant_id  = mr.tenant_id
        AND x.project_id = cp.id
        AND x.deleted_at IS NULL
        AND x.description = 'Yakunlangan ish #' || par.id::text || ' — ' || pr.name
  );

-- ──────────────────────────────────────────────────────────────────
-- Make the products visible in the warehouse's owning org so the
-- inventory list won't filter the just-inserted rows out via
-- accessibleWarehouseIds. Idempotent via ON CONFLICT.
-- ──────────────────────────────────────────────────────────────────
INSERT INTO product_organization_settings (
    tenant_id, product_id, organization_id,
    cost_price, list_price, min_price,
    min_stock_level, reorder_point, reorder_quantity
)
SELECT DISTINCT
    inv.tenant_id, inv.product_id, w.organization_id,
    COALESCE(pr.cost_price, 0),
    COALESCE(pr.list_price, 0),
    0, 0, 0, 0
FROM inventory inv
JOIN warehouses w  ON w.id = inv.warehouse_id
JOIN products   pr ON pr.id = inv.product_id AND pr.tenant_id = inv.tenant_id
JOIN material_reservations mr
  ON mr.product_id   = inv.product_id
 AND mr.warehouse_id = inv.warehouse_id
 AND mr.tenant_id    = inv.tenant_id
 AND mr.status = 'approved'
 AND mr.deleted_at IS NULL
WHERE w.organization_id IS NOT NULL
  AND pr.deleted_at IS NULL
ON CONFLICT (product_id, organization_id) DO NOTHING;
