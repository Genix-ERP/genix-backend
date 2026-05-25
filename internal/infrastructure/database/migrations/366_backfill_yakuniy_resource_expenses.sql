-- 366_backfill_yakuniy_resource_expenses.sql
--
-- Backfill: every resource sub-line attached to a YAKUNIY-confirmed
-- (approval_status = 'confirmed_engineer') work, whose consumption
-- never produced a per-resource expense row, gets one written now.
-- Inventory is also decremented in a separate step.
--
-- Why: the v2 reserve→confirm pipeline only processes resources that
-- were on the work AT SUBMIT TIME (when reservations are created).
-- Resources grafted onto a work AFTER submit (and especially after
-- engineer-confirm — see migration 365 era's "+ Qo'shimcha resurs"
-- button) bypass that flow entirely. The runtime fix in
-- processYakuniyAdHocResource handles new additions going forward;
-- this migration plugs the gap for resources already in the DB.
--
-- Rules:
--   • parent.approval_status = 'confirmed_engineer'
--   • parent.done_quantity > 0
--   • child.resource_type <> '' AND child.parent_line_id = parent.id
--   • EITHER child.norm_rate > 0 (cascade gives consumed = parent.done × norm)
--     OR child.quantity_override = TRUE AND child.quantity > 0 (manual)
--   • child.unit_rate > 0 (no point writing a zero-cost expense)
--   • NO existing expense_line whose description equals
--     'Yakunlangan ish #<parent.id> — <child.name>' — idempotent across
--     re-runs and against the runtime path that uses the same shape.
--
-- The two parts (expense INSERT and inventory UPDATE) are separate so
-- a failure in one path doesn't roll back the other. Inventory is
-- best-effort — if no matching product or no warehouse, the row is
-- silently skipped and the user can fix it manually later.

-- ───────────────────────────────────────────────────────────────────
-- STEP 1 — backfill expense_lines for missing resources
-- ───────────────────────────────────────────────────────────────────
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
      AND COALESCE(c.unit_rate, 0) > 0
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
)
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
    -- stage_id resolution prefers same-building section, falls back
    -- to name-only match (mirrors the runtime engine's logic).
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
    -- approved_by FK → employees(id); user_id may not match an employee.
    -- We leave NULL; the row's description and timestamps are enough audit.
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
  );

-- ───────────────────────────────────────────────────────────────────
-- STEP 2 — decrement inventory for the same resources
-- (best-effort; products without a matching SKU are silently skipped)
-- ───────────────────────────────────────────────────────────────────
WITH yak_resources AS (
    SELECT
        c.tenant_id,
        c.name                              AS resource_name,
        COALESCE(c.norm_rate, 0)            AS norm_rate,
        COALESCE(c.quantity, 0)             AS own_qty,
        COALESCE(c.quantity_override, FALSE) AS qty_override,
        COALESCE(p.done_quantity, 0)        AS parent_done
    FROM construction_estimate_line c
    JOIN construction_estimate_line p
      ON p.id = c.parent_line_id AND p.tenant_id = c.tenant_id
    WHERE COALESCE(p.approval_status, '') = 'confirmed_engineer'
      AND COALESCE(p.done_quantity, 0) > 0
      AND COALESCE(c.resource_type, '') <> ''
),
to_deduct AS (
    SELECT
        yr.tenant_id,
        yr.resource_name,
        SUM(
            CASE
                WHEN yr.qty_override AND yr.own_qty > 0 THEN yr.own_qty
                WHEN yr.norm_rate > 0                   THEN yr.parent_done * yr.norm_rate
                ELSE 0
            END
        ) AS consumed_total
    FROM yak_resources yr
    GROUP BY yr.tenant_id, yr.resource_name
    HAVING SUM(
        CASE
            WHEN yr.qty_override AND yr.own_qty > 0 THEN yr.own_qty
            WHEN yr.norm_rate > 0                   THEN yr.parent_done * yr.norm_rate
            ELSE 0
        END
    ) > 0
)
UPDATE inventory inv
SET quantity_on_hand = COALESCE(inv.quantity_on_hand, 0) - sub.consumed_total,
    updated_at       = NOW()
FROM to_deduct sub
JOIN products pr
  ON UPPER(pr.name) = UPPER(sub.resource_name)
 AND pr.tenant_id   = sub.tenant_id
 AND pr.deleted_at IS NULL
WHERE inv.product_id = pr.id
  AND inv.tenant_id  = pr.tenant_id;
