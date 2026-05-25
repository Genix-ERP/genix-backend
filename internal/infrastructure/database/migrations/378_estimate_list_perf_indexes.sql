-- 378_estimate_list_perf_indexes.sql
--
-- Closes the last hot-path on Smeta boshqaruvi + Bosqichlar (Stages
-- page) for tenants with hundreds of estimates and 10K+ estimate lines.
--
-- The user reports timeouts on the estimate management page and stages
-- page. Network is fine — it's data-size dependent. The two endpoints
-- behind those pages are:
--
--   1. GET /construction/projects/:id/estimates  → ListEstimates
--   2. GET /construction/estimates/:id           → GetEstimate → getEstimateLines
--
-- Both pages go through these and the Stages page calls
-- listEstimateLines multiple times per render (matched estimate + VOR
-- estimate + per-block bucketing), so any per-row cost compounds.
--
-- Companion code changes (same commit):
--
--   • ListEstimates now uses a CTE to pre-aggregate line counts
--     instead of a correlated COUNT subquery per estimate.
--
--   • getEstimateLines now fetches the parent estimate's project_id
--     and building_id once in Go and passes them as parameters,
--     eliminating three nested scalar subqueries per row in the ВОР
--     fallback. The remaining ВОР JOIN benefits from this index.
--
-- Index target: the ВОР fallback's outer filter is
--   ve.tenant_id = ? AND ve.project_id = ?
--   AND LOWER(COALESCE(ve.source_type, '')) = 'vor'
-- A partial index on (project_id, tenant_id) WHERE source_type ILIKE
-- 'vor' lets the planner prune to a handful of estimate rows in one
-- index walk; the JOIN to construction_estimate_line then uses the
-- existing idx_construction_estimate_line_estimate.
--
-- LOWER() in the partial WHERE matches the LOWER() on the read side
-- exactly, so the planner recognises it.

CREATE INDEX IF NOT EXISTS idx_construction_estimate_vor_lookup
    ON construction_estimate (project_id, tenant_id)
    WHERE LOWER(COALESCE(source_type, '')) = 'vor';

-- The CTE-based estimate line counter does
--   SELECT estimate_id, COUNT(*) FROM construction_estimate_line l
--   JOIN construction_estimate e ON e.id = l.estimate_id
--   WHERE e.project_id = $1 AND l.tenant_id = $2
--   GROUP BY l.estimate_id
-- Postgres can satisfy the GROUP BY from the existing
-- idx_construction_estimate_line_estimate (estimate_id), but only if
-- the tenant_id slice is small enough — index-only scan needs a
-- covering index. Add (tenant_id, estimate_id) as a covering composite
-- so the count is a fast index-only scan even on multi-tenant DBs.
CREATE INDEX IF NOT EXISTS idx_construction_estimate_line_tenant_estimate
    ON construction_estimate_line (tenant_id, estimate_id);

-- Refresh planner stats so the new indexes are usable on the next
-- query. ANALYZE samples rows; safe inside a migration.
ANALYZE construction_estimate;
ANALYZE construction_estimate_line;
