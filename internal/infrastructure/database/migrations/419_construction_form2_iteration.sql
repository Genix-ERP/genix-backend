-- 419_construction_form2_iteration.sql
--
-- Multi-iteration Forma 2 model.
--
-- Background
-- ----------
-- Construction estimates aren't completed in one run: a project might take
-- 18 months and the foreman submits a Forma 2 every month for the work
-- done in THAT month. Today the system stores one cumulative `done_quantity`
-- per estimate line and a one-shot `+ Forma 2 ni yaratish` button that
-- writes a frozen JSONB snapshot for reprinting — but there's no notion
-- of "this period's contribution" vs "everything before."
--
-- This migration introduces a per-project iteration series. Each press of
-- `+ Forma 2 yaratish` freezes the open iteration (status='frozen', writes
-- a construction_form2_snapshot row so Formalar tarixi automatically
-- gains an entry) and opens iteration N+1 with this-period fakt = 0 on
-- every estimate_line. `done_quantity` continues to hold the CUMULATIVE
-- value (Σ period_fakt across all iterations for that line), so every
-- existing reader — Bosqichlar progress %, Smetalar, reports — keeps
-- working without changes.
--
-- Decisions locked with the user before writing this migration:
--   1. snapshot link  → freeze writes a construction_form2_snapshot row
--   2. carry-over     → all lines flow into the new iteration, even
--                        ones already at 100% reja (period_fakt = 0)
--   3. undo policy    → freezes are permanent; no deleting frozen iters
--
-- Backfill notes
-- --------------
-- Legacy estimates already carry done_quantity > 0 from years of edits
-- before iterations existed. We create a single iteration #1 (status='open')
-- per project and stamp every estimate_line's existing done_quantity as
-- iteration #1's period_fakt. From the user's point of view, "you've been
-- working in #1 the whole time" — which is exactly the right mental model.
-- The first time they click `+ Forma 2 yaratish` after this ships, #1 gets
-- frozen with the current total and #2 opens fresh.

-- ─────────────────────────────  iteration header  ─────────────────────────

CREATE TABLE IF NOT EXISTS construction_form2_iteration (
    id              BIGSERIAL PRIMARY KEY,
    tenant_id       UUID   NOT NULL,
    project_id      BIGINT NOT NULL REFERENCES construction_projects(id) ON DELETE CASCADE,

    -- 1, 2, 3, … per project. Renders as "Forma 2 #N" in the tab strip.
    iteration_seq   INT    NOT NULL,

    -- 'open'   — exactly one row per project at any time, this is where
    --            new period_fakt writes go. Becomes 'frozen' when the user
    --            clicks `+ Forma 2 yaratish`.
    -- 'frozen' — historical; the iteration_line rows are read-only.
    status          VARCHAR(20) NOT NULL DEFAULT 'open',

    -- Set when status flips to 'frozen' — points at the snapshot row
    -- created in the same transaction so the Formalar tarixi list and the
    -- iteration tab strip stay in lockstep. NULL while open.
    snapshot_id     BIGINT REFERENCES construction_form2_snapshot(id) ON DELETE SET NULL,

    opened_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    opened_by       UUID,
    frozen_at       TIMESTAMP WITH TIME ZONE,
    frozen_by       UUID,

    CONSTRAINT chk_form2_iter_status CHECK (status IN ('open', 'frozen')),
    CONSTRAINT uq_form2_iter_seq    UNIQUE (project_id, iteration_seq)
);

-- At most ONE open iteration per project — partial unique index so
-- concurrent freeze requests can't both leave the project in 'open'
-- state. Combined with SELECT … FOR UPDATE in the handler, this makes
-- the freeze action serialisable per project.
CREATE UNIQUE INDEX IF NOT EXISTS uq_form2_iter_one_open_per_project
    ON construction_form2_iteration (project_id)
    WHERE status = 'open';

CREATE INDEX IF NOT EXISTS idx_form2_iter_tenant
    ON construction_form2_iteration (tenant_id);
CREATE INDEX IF NOT EXISTS idx_form2_iter_project_status
    ON construction_form2_iteration (project_id, status);

-- ─────────────────────────────  iteration lines  ──────────────────────────
--
-- One row per (iteration, estimate_line) holding the user-typed fakt for
-- THAT iteration only. The denormalised cumulative still lives on
-- construction_estimate_line.done_quantity (Σ period_fakt across iters)
-- so today's readers don't change.
--
-- We pre-create rows for every estimate_line when an iteration opens —
-- not lazily — so the StagesTabV2 fakt input always has a row to UPDATE
-- without a race against an INSERT path.

CREATE TABLE IF NOT EXISTS construction_form2_iteration_line (
    id                BIGSERIAL PRIMARY KEY,
    iteration_id      BIGINT NOT NULL REFERENCES construction_form2_iteration(id) ON DELETE CASCADE,
    estimate_line_id  BIGINT NOT NULL REFERENCES construction_estimate_line(id) ON DELETE CASCADE,

    -- This iteration's contribution. SUM(period_fakt) across all
    -- iterations for the same estimate_line_id = the line's cumulative
    -- done_quantity (kept in sync by the period_fakt write path).
    period_fakt       NUMERIC(20, 4) NOT NULL DEFAULT 0,

    created_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_form2_iter_line UNIQUE (iteration_id, estimate_line_id)
);

CREATE INDEX IF NOT EXISTS idx_form2_iter_line_line
    ON construction_form2_iteration_line (estimate_line_id);

-- ─────────────────────────────  backfill  ─────────────────────────────────
--
-- For every existing project, open iteration #1 and stamp each
-- estimate_line's current done_quantity as the period_fakt for that
-- iteration. This means: when the foreman first opens Bosqichlar after
-- this ships, they see "Forma 2 #1 (joriy)" containing exactly the work
-- they had already logged. Their first `+ Forma 2 yaratish` click freezes
-- #1 at that level and opens #2 with fakt = 0 across the board — which
-- is the new behaviour.
--
-- ON CONFLICT DO NOTHING on both inserts so re-running the migration on
-- a partially-applied database (shouldn't happen, but defence in depth)
-- is a no-op.

INSERT INTO construction_form2_iteration (
    tenant_id, project_id, iteration_seq, status, opened_at
)
SELECT
    p.tenant_id,
    p.id,
    1,
    'open',
    NOW()
FROM construction_projects p
ON CONFLICT (project_id, iteration_seq) DO NOTHING;

INSERT INTO construction_form2_iteration_line (
    iteration_id, estimate_line_id, period_fakt
)
SELECT
    it.id,
    el.id,
    COALESCE(el.done_quantity, 0)
FROM construction_form2_iteration it
JOIN construction_estimate          e  ON e.project_id = it.project_id
JOIN construction_estimate_line     el ON el.estimate_id = e.id
WHERE it.iteration_seq = 1
ON CONFLICT (iteration_id, estimate_line_id) DO NOTHING;

-- ─────────────────────────────  comments  ─────────────────────────────────

COMMENT ON TABLE construction_form2_iteration IS
    'Forma 2 iteration header — per-project ordered series. Exactly one open per project at any time; freezing opens the next and writes a construction_form2_snapshot row.';

COMMENT ON COLUMN construction_form2_iteration.snapshot_id IS
    'Set when status flips to frozen; points at the construction_form2_snapshot row written in the same transaction.';

COMMENT ON TABLE construction_form2_iteration_line IS
    'Per-iteration this-period fakt for each estimate_line. Σ period_fakt across all iterations for the same line = construction_estimate_line.done_quantity (kept in sync by the fakt write path).';
