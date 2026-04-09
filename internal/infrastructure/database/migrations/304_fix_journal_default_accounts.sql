-- Set default debit/credit accounts for bank and cash journals where missing
-- Bank journals → account code 1010 (Bank hisobi)
UPDATE journals j
SET default_debit_account_id = a.id,
    default_credit_account_id = a.id,
    updated_at = NOW()
FROM accounts a
WHERE j.type = 'bank'
  AND j.tenant_id = a.tenant_id
  AND COALESCE(j.organization_id, '00000000-0000-0000-0000-000000000000') = COALESCE(a.organization_id, '00000000-0000-0000-0000-000000000000')
  AND a.code = '1010'
  AND a.deleted_at IS NULL
  AND j.deleted_at IS NULL
  AND j.default_debit_account_id IS NULL;

-- Cash journals → account code 1000 (Kassa)
UPDATE journals j
SET default_debit_account_id = a.id,
    default_credit_account_id = a.id,
    updated_at = NOW()
FROM accounts a
WHERE j.type = 'cash'
  AND j.tenant_id = a.tenant_id
  AND COALESCE(j.organization_id, '00000000-0000-0000-0000-000000000000') = COALESCE(a.organization_id, '00000000-0000-0000-0000-000000000000')
  AND a.code = '1000'
  AND a.deleted_at IS NULL
  AND j.deleted_at IS NULL
  AND j.default_debit_account_id IS NULL;
