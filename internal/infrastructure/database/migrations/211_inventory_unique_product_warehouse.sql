-- Merge duplicate inventory records (keep the oldest, sum quantities, reassign transactions)
-- Then add a unique constraint to prevent future duplicates.

-- Step 1: Reassign inventory_transactions from duplicate records to the primary (oldest) record
UPDATE inventory_transactions it
SET inventory_id = keeper.id
FROM inventory dup
JOIN LATERAL (
    SELECT id FROM inventory i2
    WHERE i2.tenant_id = dup.tenant_id
      AND i2.product_id = dup.product_id
      AND i2.warehouse_id = dup.warehouse_id
      AND COALESCE(i2.variant_id, '00000000-0000-0000-0000-000000000000') = COALESCE(dup.variant_id, '00000000-0000-0000-0000-000000000000')
    ORDER BY i2.created_at ASC
    LIMIT 1
) keeper ON keeper.id != dup.id
WHERE it.inventory_id = dup.id;

-- Step 2: Merge quantity_on_hand from duplicates into the primary record
UPDATE inventory i
SET quantity_on_hand = i.quantity_on_hand + dupes.dup_qty,
    updated_at = NOW()
FROM (
    SELECT
        (SELECT id FROM inventory i2
         WHERE i2.tenant_id = inv.tenant_id
           AND i2.product_id = inv.product_id
           AND i2.warehouse_id = inv.warehouse_id
           AND COALESCE(i2.variant_id, '00000000-0000-0000-0000-000000000000') = COALESCE(inv.variant_id, '00000000-0000-0000-0000-000000000000')
         ORDER BY i2.created_at ASC LIMIT 1) as keeper_id,
        SUM(inv.quantity_on_hand) as dup_qty
    FROM inventory inv
    WHERE inv.id NOT IN (
        SELECT DISTINCT ON (tenant_id, product_id, warehouse_id, COALESCE(variant_id, '00000000-0000-0000-0000-000000000000'))
               id
        FROM inventory
        ORDER BY tenant_id, product_id, warehouse_id, COALESCE(variant_id, '00000000-0000-0000-0000-000000000000'), created_at ASC
    )
    GROUP BY keeper_id
) dupes
WHERE i.id = dupes.keeper_id AND dupes.dup_qty != 0;

-- Step 3: Delete duplicate inventory records (not the oldest per group)
DELETE FROM inventory
WHERE id NOT IN (
    SELECT DISTINCT ON (tenant_id, product_id, warehouse_id, COALESCE(variant_id, '00000000-0000-0000-0000-000000000000'))
           id
    FROM inventory
    ORDER BY tenant_id, product_id, warehouse_id, COALESCE(variant_id, '00000000-0000-0000-0000-000000000000'), created_at ASC
);

-- Step 4: Add unique constraint to prevent future duplicates
CREATE UNIQUE INDEX IF NOT EXISTS idx_inventory_unique_product_warehouse
ON inventory (tenant_id, product_id, warehouse_id, COALESCE(variant_id, '00000000-0000-0000-0000-000000000000'));
