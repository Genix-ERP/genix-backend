-- 451_form2_iteration_per_block.sql
--
-- Make the Forma 2 iteration/freeze series PER-BLOCK instead of per-project.
--
-- Background
-- ----------
-- Migration 419 introduced a multi-iteration Forma 2 series scoped to the
-- whole PROJECT: exactly one open iteration per project, one shared "Forma 2
-- #N" tab strip, and a single freeze that closed the period for every block
-- at once. In practice a Forma 2 (bajarilgan ishlar dalolatnomasi) is issued
-- per OB'EKT (block/building): the foreman freezes Block 4's acts on its own
-- schedule, independent of Block 1. Two bugs fell out of the project-level
-- model:
--   * bug 1 — the freeze snapshot picked the project's FIRST estimate as its
--     "block", so a Block 4 freeze was recorded under Block 2.
--   * bug 2 — freezing Block 4 advanced the iteration strip for ALL blocks.
--
-- This migration keys the whole iteration series on (project_id, building_id):
-- each block gets its own "#1, #2 (joriy)" strip, its own single open
-- iteration, and its own independent freeze.
--
-- building_id semantics
-- ---------------------
-- A NON-NULL BIGINT with sentinel 0 = "whole project / unassigned bucket"
-- (estimates whose building_id is NULL — legacy single-block projects). Using
-- a sentinel instead of NULL keeps the partial-unique "one open per block"
-- index correct (Postgres treats NULLs as distinct, which would otherwise
-- allow several open NULL-block iterations).
--
-- Data handling (decision locked with the user)
-- ---------------------------------------------
-- Clean per-block reset. All existing iteration rows are dropped and rebuilt
-- as one open "#1" per (project, block). No work data is lost — the cumulative
-- BAJARILDI lives on construction_estimate_line.done_quantity, which we re-seed
-- as iteration #1's period_fakt (exactly the 419 backfill model). Saved Forma 2
-- documents in construction_form2_snapshot (Formalar tarixi) are KEPT; only the
-- iteration partitioning is reset.

-- ── 1. add the block key ──────────────────────────────────────────────────
ALTER TABLE construction_form2_iteration
    ADD COLUMN IF NOT EXISTS building_id BIGINT NOT NULL DEFAULT 0;

-- ── 2. drop the project-scoped uniqueness (replaced with per-block below) ──
ALTER TABLE construction_form2_iteration
    DROP CONSTRAINT IF EXISTS uq_form2_iter_seq;
DROP INDEX IF EXISTS uq_form2_iter_one_open_per_project;

-- ── 3. clean reset ────────────────────────────────────────────────────────
-- Cascade removes construction_form2_iteration_line rows. snapshot rows are
-- untouched (FK is ON DELETE SET NULL from the iteration side), so Formalar
-- tarixi keeps every saved document.
DELETE FROM construction_form2_iteration;

-- ── 4. rebuild one OPEN iteration #1 per (project, block) ─────────────────
INSERT INTO construction_form2_iteration
    (tenant_id, project_id, building_id, iteration_seq, status, opened_at)
SELECT e.tenant_id,
       e.project_id,
       COALESCE(e.building_id, 0) AS building_id,
       1,
       'open',
       NOW()
FROM construction_estimate e
WHERE EXISTS (
    SELECT 1 FROM construction_estimate_line el WHERE el.estimate_id = e.id
)
GROUP BY e.tenant_id, e.project_id, COALESCE(e.building_id, 0);

-- ── 5. seed iteration #1 lines with the cumulative done_quantity ──────────
INSERT INTO construction_form2_iteration_line (iteration_id, estimate_line_id, period_fakt)
SELECT it.id, el.id, COALESCE(el.done_quantity, 0)
FROM construction_form2_iteration it
JOIN construction_estimate e
       ON e.project_id = it.project_id
      AND e.tenant_id  = it.tenant_id
      AND COALESCE(e.building_id, 0) = it.building_id
JOIN construction_estimate_line el ON el.estimate_id = e.id
ON CONFLICT (iteration_id, estimate_line_id) DO NOTHING;

-- ── 6. per-block uniqueness ───────────────────────────────────────────────
ALTER TABLE construction_form2_iteration
    ADD CONSTRAINT uq_form2_iter_seq UNIQUE (project_id, building_id, iteration_seq);

-- At most ONE open iteration per (project, block).
CREATE UNIQUE INDEX IF NOT EXISTS uq_form2_iter_one_open_per_block
    ON construction_form2_iteration (project_id, building_id)
    WHERE status = 'open';

CREATE INDEX IF NOT EXISTS idx_form2_iter_project_building_status
    ON construction_form2_iteration (project_id, building_id, status);
