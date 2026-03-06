-- Backfill inventory records that have NULL organization_id
-- This fixes the bug where ReceivePurchaseOrder created inventory records
-- without setting organization_id, making them invisible when the
-- ListInventory endpoint filters by organization.

UPDATE inventory i
SET organization_id = w.organization_id
FROM warehouses w
WHERE i.warehouse_id = w.id
  AND i.organization_id IS NULL
  AND w.organization_id IS NOT NULL;

-- Also backfill inventory_transactions with NULL organization_id
UPDATE inventory_transactions it
SET organization_id = inv.organization_id
FROM inventory inv
WHERE it.inventory_id = inv.id
  AND it.organization_id IS NULL
  AND inv.organization_id IS NOT NULL;
