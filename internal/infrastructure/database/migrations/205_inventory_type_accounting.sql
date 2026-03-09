-- Add inventory_type to products for GL account routing
ALTER TABLE products ADD COLUMN IF NOT EXISTS inventory_type VARCHAR(20) DEFAULT 'trade';

-- Auto-populate based on existing data
UPDATE products SET inventory_type = CASE
    WHEN type = 'service' THEN 'service'
    WHEN is_manufacturable = true THEN 'finished'
    ELSE 'trade'
END
WHERE inventory_type = 'trade' OR inventory_type IS NULL;

-- Set parent_id on sub-accounts (1310/1320/1330/1340 → parent 1300)
UPDATE accounts a SET parent_id = parent_acc.id
FROM accounts parent_acc
WHERE parent_acc.tenant_id = a.tenant_id
  AND parent_acc.organization_id = a.organization_id
  AND parent_acc.code = '1300'
  AND parent_acc.deleted_at IS NULL
  AND a.code IN ('1310', '1320', '1330', '1340')
  AND a.parent_id IS NULL
  AND a.deleted_at IS NULL;
