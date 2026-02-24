-- Outstanding payment accounts for 2-step payment posting
-- Outstanding Receipts (1150): DR on customer payment, CR when bank reconciled
-- Outstanding Payments (1160): CR on vendor payment, DR when bank reconciled

-- Create Outstanding Receipts account for each tenant+org that has a Bank Account (1100)
INSERT INTO accounts (id, tenant_id, organization_id, account_type_id, code, name, description,
    is_bank_account, is_control_account, is_reconcilable,
    current_balance, opening_balance, is_active, created_at, updated_at)
SELECT uuid_generate_v4(), a.tenant_id, a.organization_id, a.account_type_id,
    '1150', 'Outstanding Receipts', 'Customer payments awaiting bank clearing',
    false, false, true,
    0, 0, true, NOW(), NOW()
FROM accounts a
WHERE a.code = '1100' AND a.deleted_at IS NULL
AND NOT EXISTS (
    SELECT 1 FROM accounts a2
    WHERE a2.tenant_id = a.tenant_id
      AND (a2.organization_id = a.organization_id OR (a2.organization_id IS NULL AND a.organization_id IS NULL))
      AND a2.code = '1150' AND a2.deleted_at IS NULL
);

-- Create Outstanding Payments account for each tenant+org that has a Bank Account (1100)
INSERT INTO accounts (id, tenant_id, organization_id, account_type_id, code, name, description,
    is_bank_account, is_control_account, is_reconcilable,
    current_balance, opening_balance, is_active, created_at, updated_at)
SELECT uuid_generate_v4(), a.tenant_id, a.organization_id, a.account_type_id,
    '1160', 'Outstanding Payments', 'Vendor payments awaiting bank clearing',
    false, false, true,
    0, 0, true, NOW(), NOW()
FROM accounts a
WHERE a.code = '1100' AND a.deleted_at IS NULL
AND NOT EXISTS (
    SELECT 1 FROM accounts a2
    WHERE a2.tenant_id = a.tenant_id
      AND (a2.organization_id = a.organization_id OR (a2.organization_id IS NULL AND a.organization_id IS NULL))
      AND a2.code = '1160' AND a2.deleted_at IS NULL
);
