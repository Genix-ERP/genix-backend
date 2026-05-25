-- 353_work_approval_workflow.sql
--
-- Adds the foreman → supervisor → engineer approval workflow for
-- construction works (per construction_module_v2.html).
--
-- WORKFLOW
--
--   pending ─[foreman enters qty > 0]→ in_progress
--   in_progress ─[foreman: "На проверку"]→ submitted
--   submitted ─[supervisor: ✓]→ confirmed_supervisor
--             └[supervisor: ↩ reject]→ in_progress
--   confirmed_supervisor ─[engineer: ✓ Final]→ confirmed_engineer  (LOCKED)
--                        └[engineer: ↩ reject]→ submitted
--
-- Once a work reaches `confirmed_engineer` it is permanently locked —
-- no role can change quantity, status, or anything else. To "fix" a
-- finalised work the engineer must explicitly unlock it (handled by a
-- separate admin action; not part of the v2 mockup workflow itself).
--
-- DATA MODEL
--
-- The mockup treats each row in the works table as a "work" entry with
-- its own done_quantity + status. We map this onto the existing
-- construction_estimate_line table — every estimate line that lives
-- under a top-level section/stage IS a "work" in the v2 sense.
-- This avoids creating a parallel works table and keeps Form 2 / Smeta
-- boshqaruvi calculations using the same source data.

ALTER TABLE construction_estimate_line
    ADD COLUMN IF NOT EXISTS approval_status VARCHAR(40) NOT NULL DEFAULT 'pending'
        CHECK (approval_status IN (
            'pending',
            'in_progress',
            'submitted',
            'confirmed_supervisor',
            'confirmed_engineer'
        )),
    ADD COLUMN IF NOT EXISTS done_quantity NUMERIC(20, 4) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS submitted_at            TIMESTAMP WITH TIME ZONE,
    ADD COLUMN IF NOT EXISTS submitted_by            UUID,
    ADD COLUMN IF NOT EXISTS confirmed_supervisor_at TIMESTAMP WITH TIME ZONE,
    ADD COLUMN IF NOT EXISTS confirmed_supervisor_by UUID,
    ADD COLUMN IF NOT EXISTS confirmed_engineer_at   TIMESTAMP WITH TIME ZONE,
    ADD COLUMN IF NOT EXISTS confirmed_engineer_by   UUID,
    ADD COLUMN IF NOT EXISTS rejection_note          TEXT;

CREATE INDEX IF NOT EXISTS idx_construction_estimate_line_approval_status
    ON construction_estimate_line (approval_status);

CREATE INDEX IF NOT EXISTS idx_construction_estimate_line_estimate_status
    ON construction_estimate_line (estimate_id, approval_status);

COMMENT ON COLUMN construction_estimate_line.approval_status IS
    'v2 stages-page workflow: pending → in_progress → submitted → confirmed_supervisor → confirmed_engineer (locked)';
COMMENT ON COLUMN construction_estimate_line.done_quantity IS
    'Foreman-entered quantity actually completed (≤ planned quantity)';
COMMENT ON COLUMN construction_estimate_line.rejection_note IS
    'Optional reason supplied by supervisor/engineer when sending the work back to the previous step';
