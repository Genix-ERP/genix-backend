-- Give every cash/bank journal a settlement account, so recording a payment
-- does not depend on a name-and-code guess.
--
-- THE FAILURE THIS FIXES
-- RecordPayment resolved the account to debit with
--     findAccount(..., "bank account", "5110")
-- an English name and one hardcoded code. A tenant whose chart of accounts
-- says "5100 · Bank hisob raqamlari" matches neither, so every single payment
-- was refused with "Payment accounts not configured" — while the bank journal
-- the user had just selected in the dialog carried that very account in its
-- own "Tranzit schyot" field.
--
-- The handler now asks the journal first (bank_account_id → default_debit →
-- suspense). This backfills the journals that have none of the three set, so
-- existing tenants are fixed without anyone editing forms by hand.
--
-- Nothing here overwrites a configured value: every UPDATE is guarded on the
-- target column being NULL. A tenant who has deliberately pointed a journal at
-- an unusual account keeps it.

DO $$
DECLARE
  v_updated int;
BEGIN
  -- 1. Bank journals that already have a bank_account_id whose GL account is
  --    set: nothing to do, the handler resolves those directly.

  -- 2. Bank journals with nothing configured → the tenant's own settlement
  --    account, preferring a leaf.
  --
  --    The candidates are the NAS §21 family rather than one member of it, and
  --    matched on code prefix so "5110", "5110.01" and a tenant's own
  --    sub-accounts all qualify. is_leaf is preferred but not required — if a
  --    chart only defines the 5100 group, pointing at it still beats leaving
  --    the journal unusable, and resolveLeafAccount drops to a child at read
  --    time anyway.
  WITH candidate AS (
    SELECT DISTINCT ON (a.tenant_id, COALESCE(a.organization_id, '00000000-0000-0000-0000-000000000000'::uuid))
           a.tenant_id, a.organization_id, a.id
    FROM accounts a
    WHERE a.deleted_at IS NULL
      AND COALESCE(a.is_active, true) = true
      AND (a.code LIKE '511%' OR a.code LIKE '510%' OR a.code LIKE '513%')
    ORDER BY a.tenant_id,
             COALESCE(a.organization_id, '00000000-0000-0000-0000-000000000000'::uuid),
             COALESCE(a.is_leaf, true) DESC,
             a.code
  )
  UPDATE journals j
  SET suspense_account_id = c.id
  FROM candidate c
  WHERE j.tenant_id = c.tenant_id
    AND j.type = 'bank'
    AND j.deleted_at IS NULL
    AND j.bank_account_id IS NULL
    AND j.default_debit_account_id IS NULL
    AND j.suspense_account_id IS NULL;
  GET DIAGNOSTICS v_updated = ROW_COUNT;
  RAISE NOTICE '489: % bank journal(s) given a settlement account', v_updated;

  -- 3. Cash journals → Kassa (5010 family).
  WITH candidate AS (
    SELECT DISTINCT ON (a.tenant_id)
           a.tenant_id, a.id
    FROM accounts a
    WHERE a.deleted_at IS NULL
      AND COALESCE(a.is_active, true) = true
      AND a.code LIKE '501%'
    ORDER BY a.tenant_id, COALESCE(a.is_leaf, true) DESC, a.code
  )
  UPDATE journals j
  SET suspense_account_id = c.id
  FROM candidate c
  WHERE j.tenant_id = c.tenant_id
    AND j.type = 'cash'
    AND j.deleted_at IS NULL
    AND j.bank_account_id IS NULL
    AND j.default_debit_account_id IS NULL
    AND j.suspense_account_id IS NULL;
  GET DIAGNOSTICS v_updated = ROW_COUNT;
  RAISE NOTICE '489: % cash journal(s) given a settlement account', v_updated;

  -- 4. Report what is STILL unconfigured rather than pretending it is done.
  --    A journal with no 5xxx account anywhere in its tenant's chart cannot be
  --    fixed here — somebody has to create the account — and saying so is more
  --    use than a silent success.
  SELECT count(*) INTO v_updated
  FROM journals j
  WHERE j.type IN ('cash', 'bank') AND j.deleted_at IS NULL
    AND j.bank_account_id IS NULL
    AND j.default_debit_account_id IS NULL
    AND j.suspense_account_id IS NULL;
  IF v_updated > 0 THEN
    RAISE WARNING '489: % cash/bank journal(s) still have no settlement account — their tenants have no 50xx/51xx account to point at', v_updated;
  END IF;
END $$;
