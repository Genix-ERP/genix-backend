-- 417_estimate_line_is_manual.sql
--
-- Add `is_manual` flag to construction_estimate_line so the Smetalar
-- (EstimatesTab) page can show ONLY the lines that came from a bulk
-- smeta import. Lines a user added via the Smeta boshqaruvi /
-- Bosqichlar UI (CreateEstimateLine, CloneEstimateLineByCode,
-- CreateProjectResource — "+ Ish", "+ Yangi qo'shimcha etap",
-- "+ Qo'shimcha resurs", clone-by-code, "+ Resurs" in the picker) are
-- flagged is_manual = TRUE and hidden from Smetalar.
--
-- Smeta boshqaruvi and Bosqichlar deliberately keep showing both
-- buckets — they're the editing surfaces, so manual edits MUST be
-- visible there. Smetalar is the read-only "what the file said" view.

ALTER TABLE construction_estimate_line
    ADD COLUMN IF NOT EXISTS is_manual BOOLEAN NOT NULL DEFAULT FALSE;

-- Retrofit pass 1 — every line whose creation was logged to
-- construction_smeta_audit (migration 352) as one of the user-add
-- actions was created via the individual line endpoints, not bulk
-- import. Mark those as manual.
--
-- Action names match what construction_estimate.go logs:
--   subwork_add — top-level sub-stages / "+ Ish" rows
--   res_add    — "+ Qo'shimcha resurs" sub-lines
--   line_add   — generic add action used by some older paths
UPDATE construction_estimate_line el
SET is_manual = TRUE
FROM construction_smeta_audit aud
WHERE aud.line_id = el.id
  AND aud.action IN ('subwork_add', 'res_add', 'line_add')
  AND el.is_manual = FALSE;

-- Retrofit pass 2 — every line in a __catalog__ estimate (created via
-- CreateProjectResource → AddResourcePickerModal "+") is manual by
-- definition. Catalog estimates exist only to hold user-defined
-- resources that didn't come from any imported file.
UPDATE construction_estimate_line el
SET is_manual = TRUE
FROM construction_estimate e
WHERE e.id = el.estimate_id
  AND LOWER(COALESCE(e.source_type, '')) = 'catalog'
  AND el.is_manual = FALSE;

-- Helps the Smetalar query's WHERE clause; without it Postgres might
-- choose to seq-scan when filtering large estimates by is_manual.
CREATE INDEX IF NOT EXISTS idx_construction_estimate_line_is_manual
    ON construction_estimate_line (estimate_id, is_manual);

COMMENT ON COLUMN construction_estimate_line.is_manual IS
    'TRUE when the line was added by a user via the Smeta boshqaruvi / Bosqichlar UI (CreateEstimateLine, CloneEstimateLineByCode, CreateProjectResource). FALSE for lines from a bulk smeta import. The Smetalar tab filters by FALSE to show only the file-derived content.';
