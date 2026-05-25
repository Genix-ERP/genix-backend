-- 351_construction_form2_snapshot.sql
--
-- Saved Forma 2 documents for the Smeta boshqaruvi → Tarix tab.
--
-- The mockup (Form2_Works_v23_prochie_breakdown.html) lets foremen save
-- the current state of an estimate as a versioned Forma 2 snapshot so
-- they can re-open it later for review/printing without having to recreate
-- the calculation from scratch. Each snapshot captures:
--   * the period (date range) the report covers
--   * the otherCostsPct + useVat flags that were in effect
--   * a JSONB blob of the line-by-line state at save time (the user can
--     change resource prices and qtys after saving without affecting
--     the snapshot)
--   * pre-computed totals so the History list can render without
--     re-walking every line
--
-- Snapshots are immutable once created. To "edit", the user makes a new one.

CREATE TABLE IF NOT EXISTS construction_form2_snapshot (
    id              BIGSERIAL PRIMARY KEY,
    tenant_id       UUID NOT NULL,
    project_id      BIGINT NOT NULL REFERENCES construction_projects(id) ON DELETE CASCADE,
    estimate_id     BIGINT NOT NULL REFERENCES construction_estimate(id) ON DELETE CASCADE,

    period_from     DATE,
    period_to       DATE,

    other_costs_pct NUMERIC(6, 2) NOT NULL DEFAULT 0,
    use_vat         BOOLEAN       NOT NULL DEFAULT TRUE,

    -- Pre-computed totals so the Tarix list doesn't need the full lines
    -- payload to render its rows.
    total_with_vat      NUMERIC(20, 2) NOT NULL DEFAULT 0,
    total_without_vat   NUMERIC(20, 2) NOT NULL DEFAULT 0,
    construction_total  NUMERIC(20, 2) NOT NULL DEFAULT 0,
    equipment_total     NUMERIC(20, 2) NOT NULL DEFAULT 0,

    -- Optional Akt number (KS-2 reference) the user may type when saving.
    act_number      VARCHAR(100),

    -- Full snapshot JSON (lines + summary). Stored as JSONB so we can do
    -- ad-hoc queries later if needed (e.g. "find every snapshot where the
    -- prochie pct was > 5%").
    snapshot_data   JSONB NOT NULL DEFAULT '{}'::jsonb,

    created_by      UUID,
    created_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_construction_form2_snapshot_estimate
    ON construction_form2_snapshot (estimate_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_construction_form2_snapshot_project
    ON construction_form2_snapshot (project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_construction_form2_snapshot_tenant
    ON construction_form2_snapshot (tenant_id);

COMMENT ON TABLE construction_form2_snapshot IS
    'Saved Forma 2 documents (KS-2 snapshots) — created from the Smeta boshqaruvi Tarix tab.';
COMMENT ON COLUMN construction_form2_snapshot.snapshot_data IS
    'JSONB capture of the lines + summary at save time. Immutable once written.';
