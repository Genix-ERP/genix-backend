-- Backfill organization_id on warehouse_operation_types from their warehouse's organization_id
UPDATE warehouse_operation_types wot
SET organization_id = w.organization_id
FROM warehouses w
WHERE wot.warehouse_id = w.id
  AND wot.organization_id IS NULL
  AND w.organization_id IS NOT NULL;
