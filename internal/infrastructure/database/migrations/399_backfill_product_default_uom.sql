-- Backfill default unit_id / purchase_unit_id / sales_unit_id on products
-- that were created without a UOM (e.g. via the bulk import endpoint before
-- it learned to set a default). Without this, downstream UI (PO modal, BOM,
-- inventory rows) has nothing to display next to quantity.
--
-- Strategy: per tenant, pick the 'unit' (Dona) row from units_of_measure
-- seeded by migration 145 / 280 and assign it to any product columns that
-- are NULL. We only touch NULL columns — products that already have a
-- non-default UOM are left alone.

UPDATE products p
SET unit_id = u.id
FROM units_of_measure u
WHERE p.unit_id IS NULL
  AND u.tenant_id = p.tenant_id
  AND u.code = 'unit'
  AND u.is_active = true;

UPDATE products p
SET purchase_unit_id = u.id
FROM units_of_measure u
WHERE p.purchase_unit_id IS NULL
  AND u.tenant_id = p.tenant_id
  AND u.code = 'unit'
  AND u.is_active = true;

UPDATE products p
SET sales_unit_id = u.id
FROM units_of_measure u
WHERE p.sales_unit_id IS NULL
  AND u.tenant_id = p.tenant_id
  AND u.code = 'unit'
  AND u.is_active = true;
