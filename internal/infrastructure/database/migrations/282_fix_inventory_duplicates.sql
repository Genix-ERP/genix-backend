-- Fix duplicate inventory records: same tenant + product + warehouse should have ONE record.
-- Previous migration 211 attempted this but used variant_id grouping and a functional index
-- with COALESCE that doesn't properly prevent NULL duplicates in PostgreSQL.

-- Step 1: Drop the old (ineffective) unique index if it exists
DROP INDEX IF EXISTS idx_inventory_unique_product_warehouse;

-- Step 2: Reassign inventory_transactions from duplicate records to the keeper (oldest per group)
UPDATE inventory_transactions it
SET inventory_id = keeper.keeper_id
FROM (
    SELECT i.id AS dup_id, first_value(i.id) OVER (
        PARTITION BY i.tenant_id, i.product_id, i.warehouse_id
        ORDER BY i.created_at ASC
    ) AS keeper_id
    FROM inventory i
) keeper
WHERE it.inventory_id = keeper.dup_id
  AND keeper.dup_id != keeper.keeper_id;

-- Step 3: Merge quantity_on_hand from duplicates into the keeper record
WITH keepers AS (
    SELECT DISTINCT ON (tenant_id, product_id, warehouse_id)
           id, tenant_id, product_id, warehouse_id
    FROM inventory
    ORDER BY tenant_id, product_id, warehouse_id, created_at ASC
),
dup_sums AS (
    SELECT k.id AS keeper_id,
           SUM(i.quantity_on_hand) AS total_qty,
           SUM(i.quantity_reserved) AS total_reserved
    FROM inventory i
    JOIN keepers k ON k.tenant_id = i.tenant_id
                  AND k.product_id = i.product_id
                  AND k.warehouse_id = i.warehouse_id
                  AND k.id != i.id
    GROUP BY k.id
)
UPDATE inventory inv
SET quantity_on_hand = inv.quantity_on_hand + ds.total_qty,
    quantity_reserved = inv.quantity_reserved + ds.total_reserved,
    updated_at = NOW()
FROM dup_sums ds
WHERE inv.id = ds.keeper_id;

-- Step 4: Delete all duplicate rows (keep only the oldest per group)
DELETE FROM inventory
WHERE id NOT IN (
    SELECT DISTINCT ON (tenant_id, product_id, warehouse_id) id
    FROM inventory
    ORDER BY tenant_id, product_id, warehouse_id, created_at ASC
);

-- Step 5: Add a proper unique constraint (no COALESCE tricks, just the core columns)
CREATE UNIQUE INDEX idx_inventory_unique_tenant_product_warehouse
ON inventory (tenant_id, product_id, warehouse_id);
