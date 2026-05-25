-- 376_backfill_missing_yakuniy_work_expenses.sql
--
-- Backfill the per-work summary expense for YAKUNIY-confirmed parent
-- works whose finaliseMaterialsForWork run hit the
-- "construction_expense_lines_approved_by_fkey" FK violation BEFORE
-- the runtime fix landed. Symptom: Bosqichlar shows FAKT JAMI = N×rate
-- for a YAKUNIY work, but there's no `Yakunlangan ish #N — <name>`
-- expense row in construction_expense_lines, so Reja vs Fakt and the
-- Moliya → Byudjet section breakdown both show Fakt = 0 for the
-- enclosing section even though the work was confirmed.
--
-- Logic — for every parent work where:
--   • approval_status = 'confirmed_engineer'
--   • done_quantity > 0
--   • effective rate > 0 (unit_rate or sub_derived)
--   • NO `Yakunlangan ish #<work_id> —` expense exists yet
-- write the per-work summary expense at done_quantity × effective_rate.
-- Stage attribution uses the same building-aware lookup the runtime
-- and migration 374's report query both use.
--
-- approved_by is NULL (FK to employees(id) which the user_id may not
-- map to). description and approved_at are sufficient audit trail.

INSERT INTO construction_expense_lines (
    id, tenant_id, organization_id, project_id, stage_id,
    expense_date, description,
    quantity, uom, unit_price,
    amount, currency_code,
    supplier_name, status, approved_by, approved_at,
    created_by, created_at, updated_at
)
SELECT
    gen_random_uuid(),
    p.tenant_id,
    cp.organization_id,
    cp.id,
    -- Stage lookup — prefer same-building full-path match, then
    -- same-building leaf match, then any matching stage (same logic
    -- the report handler uses). Mirrors construction_reports.go's
    -- LATERAL after migration 376.
    (SELECT cs.id
     FROM construction_stages cs
     WHERE cs.tenant_id = p.tenant_id
       AND cs.project_id = cp.id
       AND (
           cs.name = COALESCE(p.parent_item_number, '')
           OR cs.name = regexp_replace(COALESCE(p.parent_item_number, ''), '^.*›\s*', '')
       )
       AND (
           e.building_id IS NULL
           OR cs.building_id IS NULL
           OR cs.building_id = e.building_id
       )
     ORDER BY
       CASE WHEN cs.building_id = e.building_id THEN 0 ELSE 1 END ASC,
       CASE WHEN cs.name = COALESCE(p.parent_item_number, '') THEN 0 ELSE 1 END ASC,
       cs.id ASC
     LIMIT 1) AS stage_id,
    COALESCE(p.confirmed_engineer_at, NOW())::date,
    'Yakunlangan ish #' || p.id::text || ' — ' || p.name,
    p.done_quantity,
    COALESCE(p.uom, ''),
    -- effective rate: stored unit_rate, then sub_derived fallback.
    -- Same fallback chain finaliseMaterialsForWork uses (and the Reja
    -- vs Fakt handler reads through `agg.rate_max`).
    GREATEST(
        COALESCE(p.unit_rate, 0),
        COALESCE(p.sub_derived, 0)
    ) AS unit_price,
    p.done_quantity * GREATEST(
        COALESCE(p.unit_rate, 0),
        COALESCE(p.sub_derived, 0)
    ) AS amount,
    'UZS',
    -- supplier name = project's company display name
    COALESCE(o.name, ''),
    'approved',
    NULL,
    COALESCE(p.confirmed_engineer_at, NOW()),
    NULL,
    COALESCE(p.confirmed_engineer_at, NOW()),
    NOW()
FROM construction_estimate_line p
JOIN construction_estimate e
  ON e.id = p.estimate_id AND e.tenant_id = p.tenant_id
JOIN construction_projects cp
  ON cp.id = e.project_id AND cp.deleted_at IS NULL
LEFT JOIN organizations o
  ON o.id = cp.organization_id
WHERE COALESCE(p.approval_status, '') = 'confirmed_engineer'
  AND COALESCE(p.done_quantity, 0) > 0
  AND COALESCE(p.parent_line_id, 0) = 0
  AND COALESCE(p.resource_type, '') = ''
  AND GREATEST(COALESCE(p.unit_rate, 0), COALESCE(p.sub_derived, 0)) > 0
  AND NOT EXISTS (
      -- Skip if a per-work expense already exists for this work id.
      -- Description prefix match — the runtime always writes
      -- 'Yakunlangan ish #<work_id> — <name>' so a prefix-match by
      -- the work id is the right uniqueness signal.
      SELECT 1
      FROM construction_expense_lines x
      WHERE x.tenant_id = p.tenant_id
        AND x.project_id = cp.id
        AND x.deleted_at IS NULL
        AND x.description LIKE 'Yakunlangan ish #' || p.id::text || ' —%'
  );
