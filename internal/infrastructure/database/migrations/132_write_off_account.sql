-- Payment Difference Write-off account (6950) for handling rounding and small payment differences
-- Uses same account type as 6900 Miscellaneous Expense (OPEX)

INSERT INTO accounts (id, tenant_id, organization_id, account_type_id, code, name, description,
    is_bank_account, is_control_account, is_reconcilable,
    current_balance, opening_balance, is_active, created_at, updated_at)
SELECT uuid_generate_v4(), a.tenant_id, a.organization_id, a.account_type_id,
    '6950', 'Payment Difference Write-off', 'Write-off for rounding and small payment differences',
    false, false, false,
    0, 0, true, NOW(), NOW()
FROM accounts a
WHERE a.code = '6900' AND a.deleted_at IS NULL
AND NOT EXISTS (
    SELECT 1 FROM accounts a2
    WHERE a2.tenant_id = a.tenant_id
      AND (a2.organization_id = a.organization_id OR (a2.organization_id IS NULL AND a.organization_id IS NULL))
      AND a2.code = '6950' AND a2.deleted_at IS NULL
);
