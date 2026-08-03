-- 468_qurilish_archive_dead_tables.sql
-- Qurilish gigiyena davomi (docs/construction-audit.md §1.1 "o'lik jadvallar").
--
-- Three 111-era tables have ZERO references in Go code and no routes; the
-- frontend wrappers that once pointed at them were removed in the 2026-08
-- cleanup. Renamed (not dropped) so any environment that accumulated rows
-- keeps its data recoverable; drop the archived_* trio in a later release
-- once nobody has asked for them.
--   - construction_work_progress  → superseded by the works 3-role flow
--   - construction_material_issues → superseded by Ombor stock operations
--   - construction_vendor_payments → superseded by S5 retention/payment plan
-- construction_cost_tracking is NOT here — ApproveMaterialRequest still
-- writes it. smeta_sections/items also stay: the list endpoint uses them as
-- the total_smeta fallback for projects without new-engine estimates.
ALTER TABLE IF EXISTS construction_work_progress RENAME TO archived_construction_work_progress;
ALTER TABLE IF EXISTS construction_material_issues RENAME TO archived_construction_material_issues;
ALTER TABLE IF EXISTS construction_vendor_payments RENAME TO archived_construction_vendor_payments;
