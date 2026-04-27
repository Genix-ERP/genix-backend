-- 356_backfill_resurs_prices.sql
--
-- One-time backfill of unit_rate (+ rate splits + total_amount) for every
-- 0-priced sub-line in non-resurs estimates, copied from the matching
-- Ресурс estimate line in the same (tenant, project).
--
-- Background: the Единич sheet has only norm + quantity columns — no
-- per-resource price column. So the Единич parser emits children with
-- unit_rate=0. Prices live in the Ресурс sheet, where each line has a
-- unit price. Users typically import all three flavours (ВОР / Единич /
-- Ресурс) for a building, but until now there was no propagation step
-- so the Smeta boshqaruvi tab kept showing NARX=0 / SUMMA=0 for every
-- Единич sub-resource even when the Ресурс estimate had the number.
--
-- The same UPDATE now runs after every BulkCreateEstimateLines (see
-- propagateResursPricesForProject in construction_estimate.go). This
-- migration applies the same logic to every existing tenant+project so
-- users don't have to re-import.
--
-- Idempotency: only touches rows where unit_rate is currently 0, so
-- manual edits via the Resurslar tab aren't overwritten. Safe to re-run.

WITH price_src AS (
    SELECT DISTINCT ON (e.tenant_id, e.project_id, LOWER(srcLine.name), COALESCE(srcLine.uom, ''))
        e.tenant_id                AS tenant_id,
        e.project_id               AS project_id,
        LOWER(srcLine.name)        AS name_key,
        COALESCE(srcLine.uom, '')  AS uom_key,
        srcLine.unit_rate          AS unit_rate,
        srcLine.material_rate      AS material_rate,
        srcLine.labor_rate         AS labor_rate,
        srcLine.equipment_rate     AS equipment_rate
    FROM construction_estimate_line srcLine
    JOIN construction_estimate e ON e.id = srcLine.estimate_id
    WHERE LOWER(COALESCE(e.source_type, '')) = 'resurs'
      AND COALESCE(srcLine.unit_rate, 0) > 0
    ORDER BY e.tenant_id,
             e.project_id,
             LOWER(srcLine.name),
             COALESCE(srcLine.uom, ''),
             srcLine.created_date DESC
)
UPDATE construction_estimate_line tgt
SET unit_rate      = ps.unit_rate,
    material_rate  = ps.material_rate,
    labor_rate     = ps.labor_rate,
    equipment_rate = ps.equipment_rate,
    total_amount   = ps.unit_rate * COALESCE(tgt.quantity, 0),
    updated_date   = NOW()
FROM price_src ps, construction_estimate tgt_e
WHERE tgt_e.id              = tgt.estimate_id
  AND tgt.tenant_id         = ps.tenant_id
  AND tgt_e.project_id      = ps.project_id
  AND LOWER(COALESCE(tgt_e.source_type, '')) <> 'resurs'
  AND tgt.parent_line_id   IS NOT NULL
  AND COALESCE(tgt.unit_rate, 0) = 0
  AND LOWER(tgt.name)       = ps.name_key
  AND COALESCE(tgt.uom, '') = ps.uom_key;
