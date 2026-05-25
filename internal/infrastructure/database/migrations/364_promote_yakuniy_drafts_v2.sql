-- 364_promote_yakuniy_drafts_v2.sql
--
-- Re-run of migration 362's draft→approved promotion. Migration 362
-- already ran successfully on first deploy, but rows that the OLD
-- per-reservation flow wrote AFTER 362 was applied (and BEFORE the
-- current single-row-per-work code shipped) stayed as draft. The
-- Moliya → Byudjet "Umumiy fakt xarajatlar" tile filters on
-- status='approved', so those drafts were invisible there even
-- though they showed up in Moliya → Xarajatlar as "Qoralama".
--
-- Filter is identical to 362 — only rows whose description starts
-- with "Yakunlangan ish #" (the prefix produced exclusively by
-- finaliseMaterialsForWork) are touched. Manual draft entries and
-- material-request drafts are intentionally left alone because
-- their workflow expects an explicit approval step.
--
-- Idempotent: re-running is a no-op once the rows are already
-- approved. After this migration the runtime path writes 'approved'
-- directly, so no further backfills are expected.

UPDATE construction_expense_lines
SET status      = 'approved',
    approved_by = COALESCE(approved_by, created_by),
    approved_at = COALESCE(approved_at, created_at),
    updated_at  = NOW()
WHERE status = 'draft'
  AND deleted_at IS NULL
  AND description LIKE 'Yakunlangan ish #%';
