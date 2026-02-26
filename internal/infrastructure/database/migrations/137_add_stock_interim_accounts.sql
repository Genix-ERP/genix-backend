-- Add Stock Interim Receipt and Stock Interim Delivery accounts for all organizations that don't have them yet.
-- These are needed for COGS journal entries on sales invoices and goods receipts.

INSERT INTO accounts (id, tenant_id, organization_id, account_type_id, code, name, description,
    is_bank_account, is_control_account, is_reconcilable, current_balance, opening_balance, is_active, created_at, updated_at)
SELECT
    gen_random_uuid(), o.tenant_id, o.id, at.id, '2230', 'Stock Interim Receipt',
    'Interim account for goods received not yet invoiced',
    false, false, false, 0, 0, true, NOW(), NOW()
FROM organizations o
CROSS JOIN account_types at
WHERE at.code = 'ST_LIAB'
AND NOT EXISTS (
    SELECT 1 FROM accounts a
    WHERE a.tenant_id = o.tenant_id AND a.organization_id = o.id
    AND (LOWER(a.name) LIKE 'stock interim receipt%' OR a.code = '2230')
    AND a.deleted_at IS NULL
)
AND o.deleted_at IS NULL;

INSERT INTO accounts (id, tenant_id, organization_id, account_type_id, code, name, description,
    is_bank_account, is_control_account, is_reconcilable, current_balance, opening_balance, is_active, created_at, updated_at)
SELECT
    gen_random_uuid(), o.tenant_id, o.id, at.id, '2231', 'Stock Interim Delivery',
    'Interim account for goods delivered not yet invoiced',
    false, false, false, 0, 0, true, NOW(), NOW()
FROM organizations o
CROSS JOIN account_types at
WHERE at.code = 'ST_LIAB'
AND NOT EXISTS (
    SELECT 1 FROM accounts a
    WHERE a.tenant_id = o.tenant_id AND a.organization_id = o.id
    AND (LOWER(a.name) LIKE 'stock interim delivery%' OR a.code = '2231')
    AND a.deleted_at IS NULL
)
AND o.deleted_at IS NULL;
