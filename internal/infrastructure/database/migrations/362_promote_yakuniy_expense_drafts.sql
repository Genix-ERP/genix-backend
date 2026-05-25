-- 362_promote_yakuniy_expense_drafts.sql
--
-- Backfill: legacy draft expense rows produced by finaliseMaterialsForWork
-- (engineer-confirm pipeline) get promoted to status='approved'. The
-- function used to insert these rows with status='draft' on the theory
-- that a separate approval step would later flip them — but YAKUNIY is
-- the actual approval gate in this flow, there is no separate review
-- step, and the Byudjet "Umumiy fakt xarajatlar" tile + per-section
-- breakdown both filter on status='approved'. Result: the foreman
-- finalised a work, the expense row was written, but Fakt stayed at 0.
--
-- The current code (after this migration) inserts as 'approved'
-- directly, so this is only needed to fix the rows already on disk.
--
-- Filter is conservative: matches only rows whose description starts
-- with "Yakunlangan ish #" (the exact prefix produced by
-- finaliseMaterialsForWork). Other draft rows — manual expense entries
-- pending review, material-request drafts — are intentionally left
-- alone because their workflow expects a manual approval step.
--
-- Idempotent: re-running is a no-op once the rows are already approved.

UPDATE construction_expense_lines
SET status      = 'approved',
    approved_by = COALESCE(approved_by, created_by),
    approved_at = COALESCE(approved_at, created_at),
    updated_at  = NOW()
WHERE status = 'draft'
  AND deleted_at IS NULL
  AND description LIKE 'Yakunlangan ish #%';
