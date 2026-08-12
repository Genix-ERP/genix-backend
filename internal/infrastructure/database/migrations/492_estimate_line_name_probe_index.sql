-- 492_estimate_line_name_probe_index.sql
--
-- Kills the last hot loop inside ListEstimateLines' ВОР NORMA fallback.
--
-- The fallback (construction_estimate.go, "Norma anchor" subquery) runs
-- once per parent row whose original_quantity is NULL/0 and probes
--
--   ... FROM construction_estimate_line vl
--   JOIN construction_estimate ve ON ve.id = vl.estimate_id
--   WHERE ve.project_id = $N AND LOWER(ve.source_type) = 'vor'
--     AND vl.name = l.name AND ...
--
-- Migration 378's partial index prunes `ve` to a handful of ВОР
-- estimates, but the `vl` side then falls back to
-- idx_construction_estimate_line_estimate (estimate_id only): every probe
-- scans ALL lines of every ВОР estimate and filters by name in the heap.
-- The (parent_item_number, name) index from 374 can't help — the
-- fallback wraps parent_item_number in COALESCE, which isn't sargable,
-- and name is the index's second column.
--
-- On legacy imports (pre-349, no original_quantity anchor) the fallback
-- fires for ~every parent row, so a single page_size=5000 request did
-- thousands of full ВОР-estimate scans — seconds per request, multiplied
-- by the Bosqichlar tab's per-block fan-out.
--
-- A composite (estimate_id, name) turns each probe into one index seek.

CREATE INDEX IF NOT EXISTS idx_construction_estimate_line_estimate_name
    ON construction_estimate_line (estimate_id, name);

-- Refresh planner stats so the index is picked up immediately.
ANALYZE construction_estimate_line;
