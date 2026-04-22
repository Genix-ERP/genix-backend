-- 339_act_source_file.sql
-- Store the source Excel file name on auto-created Forma 2 acts so a
-- multi-type import (VOR + Единич from the same file) can merge into ONE
-- Forma 2 draft. The reuse lookup in autoCreateForma2FromEstimate now
-- dedupes by (project, subcontract, building, source_file_name) instead
-- of by a fragile time window.
--
-- Behaviour:
--   - NULL for legacy rows → they never match the reuse query and stay
--     untouched when newer imports run. Desired, since we don't want a
--     fresh import silently absorbing drafts from unrelated earlier work.
--   - Filled in by autoCreateForma2FromEstimate when it creates a new
--     draft. Subsequent estimates from the same file find and extend
--     that draft.
--   - Manual / UI-created acts leave it NULL; the auto-merge logic only
--     targets auto-created drafts (which always have this set).

ALTER TABLE construction_act
    ADD COLUMN IF NOT EXISTS source_file_name VARCHAR(500);

-- Partial index: the reuse lookup only cares about draft acts with a
-- non-null source_file_name, so we keep the index tight.
CREATE INDEX IF NOT EXISTS idx_construction_act_draft_source_file
    ON construction_act (tenant_id, project_id, source_file_name)
    WHERE state = 'draft' AND source_file_name IS NOT NULL;
