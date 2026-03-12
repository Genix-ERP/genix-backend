-- Backfill inventory records with NULL organization_id from their warehouse
UPDATE inventory i
SET organization_id = w.organization_id
FROM warehouses w
WHERE i.warehouse_id = w.id
  AND i.organization_id IS NULL
  AND w.organization_id IS NOT NULL;
