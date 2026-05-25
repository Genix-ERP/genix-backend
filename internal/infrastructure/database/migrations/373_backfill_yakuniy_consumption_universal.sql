-- 373_backfill_yakuniy_consumption_universal.sql
--
-- Universal backfill driven STRAIGHT off the estimate, not off the
-- reservation table. Catches the "biton" case the user reported:
-- a resource added to a work AFTER it transitioned to 'submitted'
-- but BEFORE engineer-confirm. reserveMaterialsForWork only fires at
-- the 'submitted' transition, so resources grafted on later don't
-- get a reservation row, and finaliseMaterialsForWork's WHERE clause
-- (status='pending', estimate_line_id=workID) finds nothing for them.
--
-- The runtime fix in this commit closes that gap going forward (the
-- approval handler now also runs a finalise-from-estimate sweep). This
-- migration plugs the gap for already-confirmed works.
--
-- Logic per (tenant, product) pair where any YAKUNIY consumption
-- exists:
--   1. Sum the total expected consumption across all YAKUNIY parents.
--   2. Pick a target warehouse — project default → project's org →
--      tenant-wide.
--   3. If NO inventory row exists for this product in ANY warehouse,
--      INSERT one at -total_consumed. (Idempotent: re-runs find a row
--      and skip.)
--   4. Write any missing per-resource expense rows so the Xarajatlar /
--      Byudjet feeds reflect the cost.
--
-- We deliberately don't touch products that already HAVE inventory
-- rows — those went through a different path (reservation, manual
-- transfer, import) and we can't reliably tell what's already been
-- counted vs. what hasn't. Migration 372 handles the reservation-
-- based path; this one handles the no-reservation-no-inventory case.

-- ──────────────────────────────────────────────────────────────────
-- STEP 1 — INSERT missing inventory rows for YAKUNIY-consumed
-- products that have NO inventory rows anywhere in the tenant.
-- ──────────────────────────────────────────────────────────────────
WITH per_line AS (
    SELECT
        c.tenant_id,
        c.id AS resource_line_id,
        c.name AS resource_name,
        COALESCE(c.norm_rate, 0) AS norm_rate,
        COALESCE(c.unit_rate, 0) AS unit_rate,
        COALESCE(c.uom, '') AS uom,
        COALESCE(c.quantity, 0) AS own_qty,
        COALESCE(c.quantity_override, FALSE) AS qty_override,
        p.id AS parent_line_id,
        p.name AS parent_name,
        COALESCE(p.done_quantity, 0) AS parent_done,
        COALESCE(p.parent_item_number, '') AS section_path,
        COALESCE(p.confirmed_engineer_at, NOW()) AS confirmed_at,
        e.project_id,
        e.building_id,
        cp.organization_id AS project_org_id,
        cp.warehouse_id AS project_default_wh_id
    FROM construction_estimate_line c
    JOIN construction_estimate_line p
      ON p.id = c.parent_line_id AND p.tenant_id = c.tenant_id
    JOIN construction_estimate e
      ON e.id = c.estimate_id AND e.tenant_id = c.tenant_id
    JOIN construction_projects cp
      ON cp.id = e.project_id AND cp.deleted_at IS NULL
    WHERE COALESCE(p.approval_status, '') = 'confirmed_engineer'
      AND COALESCE(p.done_quantity, 0) > 0
      AND COALESCE(c.resource_type, '') <> ''
),
priced AS (
    SELECT pl.*,
        CASE
            WHEN qty_override AND own_qty > 0 THEN own_qty
            WHEN norm_rate > 0                THEN parent_done * norm_rate
            ELSE 0
        END AS consumed
    FROM per_line pl
),
totals AS (
    -- Sum across every YAKUNIY consumption for the same product.
    SELECT
        pr.tenant_id,
        pr.id AS product_id,
        SUM(pd.consumed) AS total_consumed,
        -- For the warehouse picker we need ONE project per group;
        -- use the smallest project id deterministically. The picker
        -- below falls back to tenant-wide if the project's org has
        -- no usable warehouse, so this rarely matters in practice.
        MIN(pd.project_org_id::text) AS any_org_text,
        MIN(pd.project_default_wh_id::text) AS any_default_wh_text
    FROM priced pd
    JOIN products pr
      ON UPPER(pr.name) = UPPER(pd.resource_name)
     AND pr.tenant_id   = pd.tenant_id
     AND pr.deleted_at IS NULL
    WHERE pd.consumed > 0
    GROUP BY pr.tenant_id, pr.id
    HAVING SUM(pd.consumed) > 0
),
needs_row AS (
    -- Only act on products that have ZERO inventory rows anywhere.
    -- If a row exists in ANY warehouse, leave alone — that's a sign
    -- some other path already wrote the deduction (or partial
    -- receives have happened) and we can't reliably reconcile.
    SELECT t.*
    FROM totals t
    WHERE NOT EXISTS (
        SELECT 1 FROM inventory inv
        WHERE inv.tenant_id  = t.tenant_id
          AND inv.product_id = t.product_id
    )
),
picked AS (
    SELECT
        nr.tenant_id,
        nr.product_id,
        nr.total_consumed,
        COALESCE(
            -- Project's default (text-uuid round-trip)
            CASE WHEN nr.any_default_wh_text <> '' AND nr.any_default_wh_text IS NOT NULL
                 THEN nr.any_default_wh_text::uuid
                 ELSE NULL
            END,
            -- Project-org's oldest active warehouse
            (SELECT w.id FROM warehouses w
              WHERE w.tenant_id = nr.tenant_id
                AND nr.any_org_text IS NOT NULL
                AND w.organization_id = nr.any_org_text::uuid
                AND COALESCE(w.is_active, TRUE) = TRUE
              ORDER BY w.created_at ASC
              LIMIT 1),
            -- Tenant-wide oldest active fallback
            (SELECT w.id FROM warehouses w
              WHERE w.tenant_id = nr.tenant_id
                AND COALESCE(w.is_active, TRUE) = TRUE
              ORDER BY w.created_at ASC
              LIMIT 1)
        ) AS warehouse_id
    FROM needs_row nr
)
INSERT INTO inventory (
    id, tenant_id, product_id, warehouse_id,
    quantity_on_hand, quantity_reserved,
    created_at, updated_at
)
SELECT
    gen_random_uuid(),
    p.tenant_id, p.product_id, p.warehouse_id,
    -p.total_consumed, 0,
    NOW(), NOW()
FROM picked p
WHERE p.warehouse_id IS NOT NULL
ON CONFLICT (tenant_id, product_id, warehouse_id) DO NOTHING;

-- ──────────────────────────────────────────────────────────────────
-- STEP 2 — write the per-resource expense rows for the same set so
-- the Xarajatlar feed and Byudjet Fakt totals catch up.
-- ──────────────────────────────────────────────────────────────────
WITH per_line AS (
    SELECT
        c.tenant_id,
        c.id AS resource_line_id,
        c.name AS resource_name,
        COALESCE(c.norm_rate, 0) AS norm_rate,
        COALESCE(c.unit_rate, 0) AS unit_rate,
        COALESCE(c.uom, '') AS uom,
        COALESCE(c.quantity, 0) AS own_qty,
        COALESCE(c.quantity_override, FALSE) AS qty_override,
        p.id AS parent_line_id,
        p.name AS parent_name,
        COALESCE(p.done_quantity, 0) AS parent_done,
        COALESCE(p.parent_item_number, '') AS section_path,
        COALESCE(p.confirmed_engineer_at, NOW()) AS confirmed_at,
        e.project_id,
        e.building_id,
        cp.organization_id
    FROM construction_estimate_line c
    JOIN construction_estimate_line p
      ON p.id = c.parent_line_id AND p.tenant_id = c.tenant_id
    JOIN construction_estimate e
      ON e.id = c.estimate_id AND e.tenant_id = c.tenant_id
    JOIN construction_projects cp
      ON cp.id = e.project_id AND cp.deleted_at IS NULL
    WHERE COALESCE(p.approval_status, '') = 'confirmed_engineer'
      AND COALESCE(p.done_quantity, 0) > 0
      AND COALESCE(c.resource_type, '') <> ''
),
priced AS (
    SELECT pl.*,
        CASE
            WHEN qty_override AND own_qty > 0 THEN own_qty
            WHEN norm_rate > 0                THEN parent_done * norm_rate
            ELSE 0
        END AS consumed
    FROM per_line pl
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
    'approved', NULL, p.confirmed_at,
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

-- ──────────────────────────────────────────────────────────────────
-- STEP 3 — link the products to the picked warehouses' orgs so the
-- newly-inserted inventory rows are visible in the products tab
-- regardless of which active company the user views as.
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
WHERE w.organization_id IS NOT NULL
  AND pr.deleted_at IS NULL
  AND EXISTS (
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
