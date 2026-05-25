-- 377_fix_double_decremented_inventory.sql
--
-- Repairs inventory rows that were decremented TWICE for the same
-- consumption event. The bug: when a resource was added to a
-- non-YAKUNIY work and then the work was YAKUNIY-confirmed,
-- finaliseMaterialsForWork ran two paths in sequence:
--
--   1. The reservation loop — marked the pending reservation
--      `approved` and decremented inventory by `quantity`.
--   2. The estimate-sweep (added in this commit) — re-walked every
--      material sub-line, didn't find a matching
--      `Yakunlangan ish #<work_id> — <resource>` expense (the
--      reservation loop only writes a per-WORK summary expense at
--      the bottom of finaliseMaterialsForWork, NOT per-resource),
--      and so decremented inventory AGAIN and wrote the per-resource
--      expense.
--
-- User-visible symptom: "test 123 — confirmed with consumption 30,
-- inventory shows −60". The runtime fix in this commit teaches the
-- estimate-sweep to also detect a same-work approved reservation as
-- "already processed", so the second decrement won't fire for new
-- confirms going forward.
--
-- This migration plugs the existing damage. For every (product,
-- warehouse) where BOTH a per-resource expense AND a same-work
-- approved reservation exist with matching quantities, add back the
-- duplicate amount to inventory.
--
-- Detection logic:
--   • material_reservations.status = 'approved'
--   • a construction_expense_lines row whose description equals
--     'Yakunlangan ish #<estimate_line_id> — <product.name>'
--   • the expense.quantity matches the reservation.quantity
--
-- That triple match is exactly the "double-debit" footprint. Cases
-- with only an expense (legitimate post-YAKUNIY ad-hoc add via
-- processYakuniyAdHocResource — no reservation involved) are
-- excluded by the JOIN. Cases with only a reservation (older code
-- before the estimate-sweep landed) are excluded too.
--
-- Idempotency: tracked by schema_migrations. Re-running this
-- migration would re-add the same amount; the migration runner
-- prevents that by recording version 377 the first time.

WITH double_debits AS (
    SELECT
        mr.tenant_id,
        mr.product_id,
        mr.warehouse_id,
        SUM(mr.quantity) AS extra_to_restore
    FROM material_reservations mr
    JOIN products pr
      ON pr.id = mr.product_id
     AND pr.tenant_id = mr.tenant_id
     AND pr.deleted_at IS NULL
    JOIN construction_expense_lines el
      ON el.tenant_id  = mr.tenant_id
     AND el.project_id = mr.project_id
     AND el.deleted_at IS NULL
     AND el.description = 'Yakunlangan ish #' || mr.estimate_line_id::text || ' — ' || pr.name
     -- Match by quantity to be sure the expense is the per-resource
     -- decrement, not a different-but-similarly-described entry.
     AND ABS(COALESCE(el.quantity, 0) - COALESCE(mr.quantity, 0)) < 0.0001
    WHERE mr.status = 'approved'
      AND mr.deleted_at IS NULL
      AND mr.warehouse_id IS NOT NULL
    GROUP BY mr.tenant_id, mr.product_id, mr.warehouse_id
)
UPDATE inventory inv
SET quantity_on_hand = COALESCE(inv.quantity_on_hand, 0) + dd.extra_to_restore,
    updated_at       = NOW()
FROM double_debits dd
WHERE inv.tenant_id    = dd.tenant_id
  AND inv.product_id   = dd.product_id
  AND inv.warehouse_id = dd.warehouse_id;
