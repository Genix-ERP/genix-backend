-- 466_qurilish_sub_act_gl.sql
-- S4: sub-akt → GL posting (developer-stsenariy) — docs/construction-roadmap.md,
-- docs/construction-integration-map.md §4 (stsenariy-qarori 2026-08-03).
--
-- A subcontractor's approved Forma-2/KS act is a COST DOCUMENT for the
-- developer: Dr 0810 (WIP) / Cr 6010 (pudratchilar), posted in the same
-- transaction as the construction_expense_lines row. journal_entry_id doubles
-- as the double-post guard; expense_line_id is the UUID bridge that
-- journal_entries.source_id points at (construction PKs are BIGINT, source_id
-- is UUID). Cancel of a posted act creates a reversal entry and marks the
-- expense line cancelled — the original links stay for the audit trail.
ALTER TABLE construction_act
    ADD COLUMN IF NOT EXISTS journal_entry_id UUID REFERENCES journal_entries(id) ON DELETE SET NULL;
ALTER TABLE construction_act
    ADD COLUMN IF NOT EXISTS expense_line_id UUID REFERENCES construction_expense_lines(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_construction_act_je
    ON construction_act(journal_entry_id)
    WHERE journal_entry_id IS NOT NULL;
