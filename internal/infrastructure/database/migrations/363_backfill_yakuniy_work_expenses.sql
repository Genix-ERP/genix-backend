-- 363_backfill_yakuniy_work_expenses.sql
--
-- Backfill: every work line that's already in YAKUNIY
-- (approval_status = 'confirmed_engineer') and has done_quantity > 0
-- gets one approved expense line equal to done × effective rate.
--
-- Why: the engineer-confirm pipeline (finaliseMaterialsForWork) used
-- to write expense lines per material reservation. Works whose
-- reservations were never created — labour-only / machine-only works,
-- or works whose product-name lookup failed — produced zero expense
-- rows even though they had real cost. Result: the Byudjet "Umumiy
-- fakt xarajatlar" tile and per-section breakdown both stayed at 0
-- after multiple YAKUNIY confirmations. The handler now writes a
-- single per-work expense row regardless of reservation state; this
-- backfill plugs the gap for existing data.
--
-- Effective rate fallback chain mirrors the runtime code:
--   1. line.unit_rate (when set)
--   2. line.total_amount / line.quantity (back-computed)
--   3. Σ(child.unit_rate × child.norm_rate) over the work's resource
--      sub-lines
--
-- Idempotency: skips works that already have an expense line whose
-- description starts with "Yakunlangan ish #<work.id> —". So if a row
-- was already written by the per-reservation flow this migration
-- doesn't double up.
--
-- Section attribution: looks up a stage by name = parent_item_number;
-- when no match exists we now auto-create a construction_stages row
-- (tagged with the work's building_id) so the per-Block Fakt total in
-- Moliya → Byudjet picks up the expense — that report INNER-JOINs
-- construction_stages and filters by building_id, so a stageless
-- expense is invisible to the per-Block view. The synthetic
-- "(Boshqalar)" reconciliation row still catches anything that slips
-- through.
--
-- Step 1 — auto-create missing stages for every (project, building,
-- section_path) combo present in YAKUNIY works. Skips combos that
-- already have a stage row matching the same (project, name,
-- building_id) tuple. Building-aware because the same section name
-- can legitimately exist in two different blocks.

INSERT INTO construction_stages (
    tenant_id, project_id, building_id, name,
    stage_order, status, planned_budget,
    created_at, updated_at
)
SELECT
    src.tenant_id, src.project_id, src.building_id, src.section_path,
    COALESCE((
        SELECT MAX(s2.stage_order) + 1
        FROM construction_stages s2
        WHERE s2.tenant_id = src.tenant_id AND s2.project_id = src.project_id
    ), 1),
    'pending', 0,
    NOW(), NOW()
FROM (
    SELECT DISTINCT
        l.tenant_id                AS tenant_id,
        e.project_id               AS project_id,
        e.building_id              AS building_id,
        l.parent_item_number       AS section_path
    FROM construction_estimate_line l
    JOIN construction_estimate e ON e.id = l.estimate_id
    WHERE COALESCE(l.approval_status, '') = 'confirmed_engineer'
      AND COALESCE(l.parent_line_id, 0) = 0
      AND COALESCE(l.resource_type, '') = ''
      AND COALESCE(l.done_quantity, 0) > 0
      AND COALESCE(l.parent_item_number, '') <> ''
) src
WHERE NOT EXISTS (
    SELECT 1 FROM construction_stages s
    WHERE s.tenant_id  = src.tenant_id
      AND s.project_id = src.project_id
      AND s.name       = src.section_path
      AND (
          (s.building_id IS NULL AND src.building_id IS NULL)
          OR s.building_id = src.building_id
          OR s.building_id IS NULL  -- legacy stage already covers this section
      )
);

-- Step 2 — backfill missing building_id on stages whose name uniquely
-- matches a section in exactly one building's estimate. This rescues
-- stages created before migration 333 added building_id; without it,
-- the per-Block Fakt total INNER-JOIN with `s.building_id = $3` would
-- still skip them. We only set building_id when the section name
-- resolves to a SINGLE building across the project's estimates — if
-- the same section name lives in two blocks we leave the stage alone
-- to avoid mis-attribution (the auto-create above already added a
-- correctly-tagged duplicate where needed).

UPDATE construction_stages s
SET building_id = sub.bid, updated_at = NOW()
FROM (
    SELECT
        l.tenant_id  AS tenant_id,
        e.project_id AS project_id,
        l.parent_item_number AS section_path,
        MAX(e.building_id) AS bid,
        COUNT(DISTINCT e.building_id) FILTER (WHERE e.building_id IS NOT NULL) AS distinct_bids
    FROM construction_estimate_line l
    JOIN construction_estimate e ON e.id = l.estimate_id
    WHERE COALESCE(l.parent_item_number, '') <> ''
      AND e.building_id IS NOT NULL
    GROUP BY l.tenant_id, e.project_id, l.parent_item_number
) sub
WHERE s.building_id IS NULL
  AND s.tenant_id  = sub.tenant_id
  AND s.project_id = sub.project_id
  AND s.name       = sub.section_path
  AND sub.distinct_bids = 1;

-- Step 3 — main expense backfill (unchanged logic, now finds stages
-- thanks to the steps above).

WITH effective AS (
    SELECT
        l.id                       AS work_id,
        l.tenant_id                AS tenant_id,
        e.project_id               AS project_id,
        e.building_id              AS building_id,
        cp.organization_id         AS organization_id,
        l.parent_item_number       AS section_path,
        COALESCE(l.name, '')       AS work_name,
        COALESCE(l.uom, '')        AS work_uom,
        COALESCE(l.done_quantity, 0) AS done_qty,
        COALESCE(l.quantity, 0)    AS qty,
        COALESCE(l.unit_rate, 0)   AS unit_rate,
        COALESCE(l.total_amount, 0) AS total_amount,
        COALESCE(l.confirmed_engineer_at, NOW()) AS confirmed_at,
        COALESCE(l.confirmed_engineer_by, l.created_by) AS confirmed_by,
        COALESCE((
            SELECT SUM(COALESCE(s.unit_rate, 0) * COALESCE(s.norm_rate, 0))
            FROM construction_estimate_line s
            WHERE s.parent_line_id = l.id AND s.tenant_id = l.tenant_id
              AND COALESCE(s.resource_type, '') <> ''
        ), 0) AS sub_derived_rate
    FROM construction_estimate_line l
    JOIN construction_estimate e ON e.id = l.estimate_id
    -- organization_id lives on construction_projects, not on
    -- construction_estimate. Joining through the project gives us the
    -- right org without depending on a column that doesn't exist on
    -- the estimate header.
    JOIN construction_projects cp ON cp.id = e.project_id
    WHERE COALESCE(l.approval_status, '') = 'confirmed_engineer'
      AND COALESCE(l.parent_line_id, 0) = 0
      AND COALESCE(l.resource_type, '') = ''
      AND COALESCE(l.done_quantity, 0) > 0
),
priced AS (
    SELECT
        ef.*,
        CASE
            WHEN ef.unit_rate > 0 THEN ef.unit_rate
            WHEN ef.qty > 0 AND ef.total_amount > 0 THEN ef.total_amount / ef.qty
            ELSE ef.sub_derived_rate
        END AS effective_rate
    FROM effective ef
),
to_insert AS (
    SELECT
        p.*,
        p.done_qty * p.effective_rate AS amount,
        (
            -- Prefer a stage that lives in the same building as the
            -- estimate; fall back to a name-only match for legacy data
            -- where construction_stages rows weren't tagged with a
            -- building_id. The ORDER BY pushes same-building hits to
            -- the top so the LIMIT 1 picks them when both exist.
            SELECT s.id
            FROM construction_stages s
            WHERE s.tenant_id = p.tenant_id
              AND s.project_id = p.project_id
              AND s.name = p.section_path
              AND (p.building_id IS NULL OR s.building_id IS NULL OR s.building_id = p.building_id)
            ORDER BY
                CASE WHEN p.building_id IS NOT NULL AND s.building_id = p.building_id THEN 0 ELSE 1 END ASC,
                s.id ASC
            LIMIT 1
        ) AS stage_id,
        (SELECT COALESCE(name, '') FROM organizations WHERE id = p.organization_id) AS supplier_name
    FROM priced p
    WHERE p.done_qty * p.effective_rate > 0
      AND NOT EXISTS (
          SELECT 1 FROM construction_expense_lines x
          WHERE x.tenant_id = p.tenant_id
            AND x.project_id = p.project_id
            AND x.deleted_at IS NULL
            AND x.description LIKE 'Yakunlangan ish #' || p.work_id::text || ' —%'
      )
)
INSERT INTO construction_expense_lines (
    id, tenant_id, organization_id, project_id, stage_id,
    expense_date, description,
    quantity, uom, unit_price,
    amount, currency_code,
    supplier_name, status, approved_by, approved_at,
    created_by, created_at, updated_at
)
SELECT
    gen_random_uuid(), tenant_id, organization_id, project_id, stage_id,
    confirmed_at::date,
    'Yakunlangan ish #' || work_id::text || ' — ' || work_name,
    done_qty, work_uom, effective_rate,
    amount, 'UZS',
    supplier_name, 'approved', confirmed_by, confirmed_at,
    confirmed_by, confirmed_at, NOW()
FROM to_insert;
