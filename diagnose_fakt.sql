-- diagnose_fakt.sql
-- Diagnoses why "Umumiy fakt xarajatlar" stays at 0 for project 1, building 2.
-- Run with:
--   psql "postgres://genix:genix_secret@localhost:5432/genixerp" -f diagnose_fakt.sql
-- (adjust credentials if yours differ)

\echo
\echo '======================================================================'
\echo 'Q1 - YAKUNIY works in project 1 (all blocks). effective_rate hint.'
\echo '======================================================================'
SELECT
    l.id,
    LEFT(COALESCE(l.name,''), 40) AS name,
    l.parent_line_id,
    LEFT(COALESCE(l.parent_item_number,''), 30) AS section,
    l.done_quantity AS done,
    l.unit_rate     AS uprice,
    l.total_amount  AS total,
    l.quantity      AS qty,
    e.building_id   AS bid,
    (SELECT COALESCE(SUM(COALESCE(s.unit_rate,0) * COALESCE(s.norm_rate,0)), 0)
     FROM construction_estimate_line s
     WHERE s.parent_line_id = l.id AND s.tenant_id = l.tenant_id
       AND COALESCE(s.resource_type,'') <> '') AS sub_rate
FROM construction_estimate_line l
JOIN construction_estimate e ON e.id = l.estimate_id
WHERE e.project_id = 1
  AND COALESCE(l.approval_status,'') = 'confirmed_engineer'
  AND COALESCE(l.resource_type,'') = ''
  AND COALESCE(l.done_quantity,0) > 0
ORDER BY e.building_id, l.parent_item_number, l.id;

\echo
\echo '======================================================================'
\echo 'Q2 - Expense rows written by migration 363 / runtime for project 1.'
\echo '======================================================================'
SELECT
    el.id,
    el.stage_id,
    LEFT(el.description, 60) AS description,
    el.amount,
    el.status,
    el.deleted_at IS NOT NULL AS deleted
FROM construction_expense_lines el
WHERE el.project_id = 1
  AND el.description LIKE 'Yakunlangan ish #%'
ORDER BY el.created_at DESC
LIMIT 50;

\echo
\echo '======================================================================'
\echo 'Q2b - Total approved expenses for project 1, all blocks.'
\echo '======================================================================'
SELECT
    COUNT(*) AS rows_count,
    COALESCE(SUM(amount), 0) AS sum_amount
FROM construction_expense_lines
WHERE project_id = 1
  AND status = 'approved'
  AND deleted_at IS NULL;

\echo
\echo '======================================================================'
\echo 'Q3 - Stages in project 1, with building_id tagging.'
\echo '======================================================================'
SELECT id, building_id AS bid, LEFT(name, 60) AS name, status
FROM construction_stages
WHERE project_id = 1
ORDER BY building_id NULLS FIRST, name;

\echo
\echo '======================================================================'
\echo 'Q4 - Per-Block actual using the EXACT report query (project 1, block 2).'
\echo '======================================================================'
SELECT COALESCE(SUM(el.amount), 0) AS per_block_actual
FROM construction_expense_lines el
JOIN construction_stages s
  ON s.id = el.stage_id AND s.tenant_id = el.tenant_id
WHERE el.project_id = 1
  AND el.status = 'approved' AND el.deleted_at IS NULL
  AND (
    s.building_id = 2
    OR EXISTS (
      SELECT 1
      FROM construction_estimate_line ll
      JOIN construction_estimate ee ON ee.id = ll.estimate_id
      WHERE ll.tenant_id = el.tenant_id
        AND ee.project_id = el.project_id
        AND ee.building_id = 2
        AND ll.parent_item_number = s.name
      LIMIT 1
    )
  );

\echo
\echo '======================================================================'
\echo 'Q5 - Has migration 363 been recorded as applied?'
\echo '======================================================================'
SELECT version, name, applied_at
FROM schema_migrations
WHERE version IN (333, 353, 354, 355, 360, 361, 362, 363)
ORDER BY version;
