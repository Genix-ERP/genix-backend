-- 374_reja_fakt_perf_indexes.sql
--
-- Indexes to speed up the Reja vs Fakt page on real-world projects.
-- Production deployment was timing out on
--   GET /construction/projects/10/reja-fakt?building_id=13
-- which then surfaced as a CORS error in the browser (the gateway
-- rewrites timed-out responses without CORS headers, so Chrome
-- reports it as a CORS failure rather than a 504).
--
-- The handler runs three patterns that touch construction_estimate_line
-- repeatedly:
--   • per_line CTE — pulls every top-level work in the project, with a
--     correlated SUM over its sub-resources (parent_line_id = l.id).
--   • a separate sub-resource query — parent_line_id = ANY($parents)
--     filtered to resource_type <> ''.
--   • the sub-stage LATERAL join — el.estimate_id IN (project's estimates)
--     plus parent_item_number = stage.name and name = sub_stage.name.
--
-- All three benefit from these composite indexes:
--   1. (parent_line_id) partial index — speeds the correlated SUM.
--      idx_construction_estimate_line_estimate already covers
--      estimate_id but parent_line_id has no index of its own, which
--      makes the correlated subquery a sequential scan per parent on
--      large projects (3000+ lines = 9M comparisons).
--   2. (estimate_id, parent_line_id) — accelerates the top-level
--      "WHERE estimate_id IN (...) AND parent_line_id = 0" scan in
--      the synthetic-projection SELECT and the LATERAL match.
--   3. (parent_item_number, name) — used by the LATERAL match
--      between estimate_line and construction_sub_stages.
--
-- All indexes use IF NOT EXISTS so re-runs are safe.

-- Speeds the correlated SUM in per_line:
--   SELECT SUM(...) FROM construction_estimate_line s
--   WHERE s.parent_line_id = l.id AND s.tenant_id = l.tenant_id
--     AND COALESCE(s.resource_type, '') <> ''
-- Partial index on rows with a parent — that's the rows we actually
-- read in the subquery — keeps the index small and selective.
CREATE INDEX IF NOT EXISTS idx_construction_estimate_line_parent
    ON construction_estimate_line (parent_line_id, tenant_id)
    WHERE parent_line_id IS NOT NULL;

-- Speeds the "top-level work in scope" scan:
--   WHERE l.estimate_id IN (...) AND l.parent_line_id = 0
-- Composite (estimate_id, parent_line_id) lets Postgres pick the
-- exact slice in one index walk instead of estimating a btree on
-- estimate_id and then filtering parent_line_id=0 row-by-row.
CREATE INDEX IF NOT EXISTS idx_construction_estimate_line_estimate_parent
    ON construction_estimate_line (estimate_id, parent_line_id);

-- Speeds the sub-stage LATERAL match:
--   WHERE COALESCE(el.parent_item_number, '') = s.name
--     AND el.name = ss.name
-- Two separate-column index works because Postgres can probe the
-- first column and bitmap-AND with the second. We use plain text
-- columns (no COALESCE wrapper) so the index is usable; the handler
-- can safely treat NULL parent_item_number as not-matching since the
-- stage's `name` is always non-empty.
CREATE INDEX IF NOT EXISTS idx_construction_estimate_line_name_section
    ON construction_estimate_line (parent_item_number, name)
    WHERE parent_item_number IS NOT NULL;

-- Touch up planner stats so the new indexes are usable on the next
-- query without waiting for autovacuum to catch up. ANALYZE on a big
-- table is fast (it samples rows) and is safe to run inside a
-- migration.
ANALYZE construction_estimate_line;
