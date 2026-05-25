-- Migration 413: display-only "imported" columns on construction_estimate_line
--
-- The Ресурс XLSX import previously persisted only name / uom / unit_rate
-- (via material_rate, labor_rate, equipment_rate) from each line, and dropped
-- the file's "Количество" and "Сметная стоимость / в базисном уровне" columns
-- because they're computed by the rest of the system (quantity is the live
-- FAKT ledger; total_amount = unit_rate × quantity).
--
-- The user wants these source-file values visible in the UI WITHOUT them
-- feeding any business logic (cost cascade, NORMA pill, Reja vs Fakt budget,
-- approval, top-ups, …). The cleanest way is to mirror them into a pair of
-- dedicated columns that ONLY the read path surfaces and that no trigger,
-- view, or aggregation touches.
--
--   imported_quantity  → Количество from the file (column 4 on Ресурс)
--   imported_total     → Сметная стоимость / в базисном уровне (right-most
--                        total column)
--
-- Both are nullable so legacy rows imported before this migration stay valid
-- and so non-resurs imports (Единич / ВОР) that don't carry these fields
-- leave them NULL. NUMERIC(20,4) matches the precision used elsewhere for
-- estimate amounts (e.g. budget_total in migration 369). The bulk insert in
-- BulkCreateEstimateLines populates them; getEstimateLines and
-- ListEstimateLines return them; the frontend Smetalar resurs table renders
-- the values verbatim.

ALTER TABLE construction_estimate_line
    ADD COLUMN IF NOT EXISTS imported_quantity NUMERIC(20,4),
    ADD COLUMN IF NOT EXISTS imported_total    NUMERIC(20,4);

COMMENT ON COLUMN construction_estimate_line.imported_quantity IS
    'Display-only: original "Количество" value from the imported Ресурс XLSX. '
    'Never used in cost calculations or ledger updates — surfaced only by '
    'the read path so the UI can show the file figure alongside the live '
    'quantity (which Единич/Ресурс template imports force to 0).';

COMMENT ON COLUMN construction_estimate_line.imported_total IS
    'Display-only: original "Сметная стоимость в базисном уровне" total from '
    'the imported Ресурс XLSX. Never used in cost calculations — surfaced '
    'only by the read path so the UI can show the file figure alongside the '
    'computed total_amount.';
