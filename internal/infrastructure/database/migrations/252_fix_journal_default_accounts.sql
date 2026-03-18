-- Fix existing cash/bank journals missing profit/loss accounts and payment method outstanding accounts

-- Set profit_account_id for cash/bank journals (use 6900 or 4000 fallback)
UPDATE journals j
SET profit_account_id = (
    SELECT a.id FROM accounts a
    WHERE a.tenant_id = j.tenant_id
      AND a.organization_id = j.organization_id
      AND a.code IN ('6900', '4000', '7100')
      AND a.deleted_at IS NULL
    ORDER BY CASE a.code WHEN '6900' THEN 0 WHEN '4000' THEN 1 ELSE 2 END
    LIMIT 1
)
WHERE j.type IN ('cash', 'bank')
  AND j.profit_account_id IS NULL
  AND j.deleted_at IS NULL;

-- Set loss_account_id for cash/bank journals (use 9400 or 6000 fallback)
UPDATE journals j
SET loss_account_id = (
    SELECT a.id FROM accounts a
    WHERE a.tenant_id = j.tenant_id
      AND a.organization_id = j.organization_id
      AND a.code IN ('9400', '6000', '7200')
      AND a.deleted_at IS NULL
    ORDER BY CASE a.code WHEN '9400' THEN 0 WHEN '6000' THEN 1 ELSE 2 END
    LIMIT 1
)
WHERE j.type IN ('cash', 'bank')
  AND j.loss_account_id IS NULL
  AND j.deleted_at IS NULL;

-- Set outstanding_account_id for BANK journal payment methods (use 1010 bank account)
UPDATE journal_payment_methods jpm
SET outstanding_account_id = (
    SELECT a.id FROM accounts a
    JOIN journals j2 ON j2.tenant_id = a.tenant_id AND j2.organization_id = a.organization_id
    WHERE j2.id = jpm.journal_id
      AND a.code = '1010'
      AND a.deleted_at IS NULL
    LIMIT 1
)
FROM journals j
WHERE j.id = jpm.journal_id
  AND j.type = 'bank'
  AND jpm.outstanding_account_id IS NULL
  AND j.deleted_at IS NULL;

-- Set outstanding_account_id for CASH journal payment methods (use 1000 cash account)
UPDATE journal_payment_methods jpm
SET outstanding_account_id = (
    SELECT a.id FROM accounts a
    JOIN journals j2 ON j2.tenant_id = a.tenant_id AND j2.organization_id = a.organization_id
    WHERE j2.id = jpm.journal_id
      AND a.code = '1000'
      AND a.deleted_at IS NULL
    LIMIT 1
)
FROM journals j
WHERE j.id = jpm.journal_id
  AND j.type = 'cash'
  AND jpm.outstanding_account_id IS NULL
  AND j.deleted_at IS NULL;
