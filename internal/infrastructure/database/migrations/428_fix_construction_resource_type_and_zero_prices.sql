-- 421_fix_construction_resource_type_and_zero_prices.sql
--
-- Data correction for construction estimate resources mis-imported by the
-- Ресурс-sheet parser. That parser classified each row purely by its section
-- header and defaulted to 'material', so machine (МАШ-Ч) and labour (ЧЕЛ-Ч)
-- resources that landed before/under an undetected header were stored with
-- the WRONG resource_type and, in some files, a 0 unit_rate. Symptoms:
--   * the same resource showing twice in the "Qo'shimcha resurs" picker
--     (once correct from the Единич import, once as material/0), and
--   * 0-price resources in the Forma 2 creation modal.
--
-- The frontend parser is fixed going forward (SmetaImportModal.parseResurs
-- now classifies by UoM). This migration repairs the already-imported rows.
-- It is idempotent — re-running it changes nothing once the data is correct.

-- 1a. Machine resources (UoM contains МАШ ... Ч, i.e. machine-hours) → 'equipment'.
UPDATE construction_estimate_line
SET resource_type = 'equipment',
    updated_date  = NOW()
WHERE UPPER(COALESCE(uom, '')) LIKE '%МАШ%'
  AND UPPER(COALESCE(uom, '')) LIKE '%Ч%'
  AND LOWER(COALESCE(resource_type, '')) NOT IN ('equipment', 'machine', 'mashina', 'masina');

-- 1b. Labour resources (UoM contains ЧЕЛ ... Ч, i.e. man-hours) → 'labor'.
UPDATE construction_estimate_line
SET resource_type = 'labor',
    updated_date  = NOW()
WHERE UPPER(COALESCE(uom, '')) LIKE '%ЧЕЛ%'
  AND UPPER(COALESCE(uom, '')) LIKE '%Ч%'
  AND LOWER(COALESCE(resource_type, '')) <> 'labor';

-- 2. Backfill 0-price resource lines from a priced twin of the SAME
--    (project, name, uom). Only resource rows (resource_type set) are
--    touched, and only when a real price exists elsewhere in the same
--    project — genuinely unpriced resources (no priced twin) stay at 0.
WITH priced AS (
    SELECT e.project_id,
           el.name,
           COALESCE(el.uom, '') AS uom,
           MAX(el.unit_rate)    AS price
    FROM construction_estimate_line el
    JOIN construction_estimate e ON e.id = el.estimate_id
    WHERE COALESCE(el.unit_rate, 0) > 0
    GROUP BY e.project_id, el.name, COALESCE(el.uom, '')
)
UPDATE construction_estimate_line t
SET unit_rate    = p.price,
    total_amount = p.price * COALESCE(t.quantity, 0),
    updated_date = NOW()
FROM construction_estimate e2, priced p
WHERE t.estimate_id = e2.id
  AND e2.project_id = p.project_id
  AND t.name = p.name
  AND COALESCE(t.uom, '') = p.uom
  AND COALESCE(t.unit_rate, 0) = 0
  AND COALESCE(t.resource_type, '') <> '';
