-- 448_fix_resurs_material_type_from_section.sql
--
-- Fixes a stale material-type misclassification in Ресурс (resource) estimates.
--
-- Estimates imported BEFORE the "section-authoritative" import fix used a
-- name-based classifier (classifyMaterialByName) that tagged МАТЕРИАЛЬНЫЕ-section
-- items like РОЗЕТКА / СВЕТИЛЬНИК / ВЫКЛЮЧАТЕЛЬ as material_type='equipment'
-- (or 'cable'). Those lines then dropped out of the "Material" bucket in the
-- Resurslar normasi report and landed in "Uskuna" / "Kabel", so the materials
-- total came out short (e.g. ~30.5M per block on project 19) and no longer
-- matched the Excel Свод "Строительные материалы" line.
--
-- The import preserves the section header in `parent_item_number` for resurs
-- lines, so we can re-derive material_type from the section deterministically —
-- exactly what the current importer does. This is general (all tenants /
-- companies / projects); no file- or project-specific targeting.
--
-- Scope guard: only touch estimates that actually have explicit КАБЕЛЬНАЯ /
-- ОБОРУДОВАНИЕ sections. Single-bucket files (one big МАТЕРИАЛЬНЫЕ РЕСУРСЫ
-- section) legitimately rely on the name classifier to split the occasional
-- inline cable/equipment item, so they are left untouched.
--
-- Material-family only: labour (resource_type='labor') and machines
-- (resource_type='equipment' / МАШ-Ч) are excluded — their material_type is
-- irrelevant and must not change.
--
-- Idempotent: re-running recomputes the same value from the (immutable) section.

UPDATE construction_estimate_line l
SET material_type = CASE
        WHEN UPPER(COALESCE(l.parent_item_number, '')) LIKE '%КАБЕЛ%'      THEN 'cable'
        WHEN UPPER(COALESCE(l.parent_item_number, '')) LIKE '%ОБОРУДОВАН%' THEN 'equipment'
        ELSE 'standard'
    END
FROM construction_estimate e
WHERE e.id = l.estimate_id
  AND LOWER(COALESCE(e.source_type, '')) = 'resurs'
  AND LOWER(COALESCE(l.resource_type, '')) = 'material'
  AND COALESCE(l.parent_item_number, '') <> ''
  AND UPPER(COALESCE(l.uom, '')) NOT LIKE '%ЧЕЛ%'
  AND UPPER(COALESCE(l.uom, '')) NOT LIKE '%МАШ%'
  AND EXISTS (
      SELECT 1
      FROM construction_estimate_line x
      WHERE x.estimate_id = l.estimate_id
        AND (UPPER(COALESCE(x.parent_item_number, '')) LIKE '%КАБЕЛ%'
             OR UPPER(COALESCE(x.parent_item_number, '')) LIKE '%ОБОРУДОВАН%')
  )
  AND l.material_type IS DISTINCT FROM (CASE
        WHEN UPPER(COALESCE(l.parent_item_number, '')) LIKE '%КАБЕЛ%'      THEN 'cable'
        WHEN UPPER(COALESCE(l.parent_item_number, '')) LIKE '%ОБОРУДОВАН%' THEN 'equipment'
        ELSE 'standard'
    END);
