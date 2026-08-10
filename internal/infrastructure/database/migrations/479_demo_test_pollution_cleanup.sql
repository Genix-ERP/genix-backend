-- 474: Demo tenant test-pollution cleanup (docs/moliya-v2/audit.md §2).
--
-- RENUMBERED 474 -> 479 during the dev merge. The original number was already
-- taken on main, and schema_migrations.version is a PRIMARY KEY: two files at
-- one version both enter `pending`, both apply, and the second INSERT violates
-- the key — RunMigrations then fails and the API crash-loops on boot. On a
-- database that already recorded that version the loser is skipped forever
-- instead, and its schema change silently never lands.
--
-- The pytest suite ran ~65-67 full sessions against the live dev DB and left:
--   * 67 bank_accounts 'Test Bank UZS'/'NBU Test' (conftest.py session fixture,
--     no teardown), all account_id IS NULL, balance column summing 455M that
--     exists nowhere in the GL;
--   * 198 test payments on those accounts (refs PAY-TEST-*/SUP-PAY-*/CONF-*),
--     52 of which posted real journal entries (Dr 5110 / Cr 4010, 52M);
--   * 130 bank_transactions (reference TEST-INC-001/TEST-EXP-001);
--   * 65 budgets 'Test Byudjet BUD-%' (zero budget_lines);
--   * 192 contacts 'Test Yetkazuvchi MChJ'/'Test Xaridor MChJ' (FK-referenced
--     by real-looking documents -> soft-delete only);
--   * 42 procurement_contracts 'Test shartnoma %'.
--
-- Every statement is scoped to the demo tenant AND the exact fixture patterns,
-- so a real tenant can never match. Deleting the 52 posted JEs is safe here
-- (proven test residue) but blocked by the immutability triggers, which we
-- disable for this transaction only. The deferred balance trigger stays on:
-- deleting an entry together with all its lines is balance-neutral.

DO $cleanup$
DECLARE
  v_tenant uuid;
  v_je_count int;
  v_leftover int;
  v_dr numeric;
  v_cr numeric;
  v_dr_before numeric;
  v_cr_before numeric;
BEGIN
  SELECT id INTO v_tenant FROM tenants WHERE code = 'demo';
  IF v_tenant IS NULL THEN
    RAISE NOTICE '474: no demo tenant, nothing to clean';
    RETURN;
  END IF;

  -- The trial balance BEFORE anything is touched.
  --
  -- The first version of this migration asserted Dr = Cr afterwards, full stop.
  -- That held on the dev database it was written against and crash-looped
  -- production, where the demo tenant's ledger was ALREADY out of balance by
  -- 154 285,7142 before this file ran — 1 080 000 / 7, a pre-existing entry
  -- split into sevenths whose rounding remainder was dropped.
  --
  -- The assertion was answering the wrong question. This migration deletes
  -- journal entries TOGETHER WITH all of their lines, which is balance-neutral
  -- by construction, so it cannot create an imbalance and has no business
  -- refusing to run because of one it inherited. What it must guarantee is that
  -- it does not make things worse — so the check below compares the imbalance
  -- before and after and requires it to be unchanged. A pre-existing gap is
  -- reported as a warning and left alone: inventing a balancing entry in
  -- someone's ledger without knowing what caused the gap would be worse than
  -- the gap.
  SELECT COALESCE(SUM(jel.debit_amount), 0), COALESCE(SUM(jel.credit_amount), 0)
    INTO v_dr_before, v_cr_before
  FROM journal_entry_lines jel
  JOIN journal_entries je ON je.id = jel.journal_entry_id
  WHERE je.tenant_id = v_tenant AND je.status = 'posted' AND je.deleted_at IS NULL;

  IF v_dr_before <> v_cr_before THEN
    RAISE WARNING '474: demo trial balance was ALREADY out of balance before cleanup by % (Dr % <> Cr %) — pre-existing, not caused here; investigate separately',
      v_dr_before - v_cr_before, v_dr_before, v_cr_before;
  END IF;

  -- Target graph -------------------------------------------------------
  CREATE TEMP TABLE tmp_test_bank_accounts ON COMMIT DROP AS
    SELECT id FROM bank_accounts
    WHERE tenant_id = v_tenant AND deleted_at IS NULL
      AND name = 'Test Bank UZS' AND bank_name = 'NBU Test';

  CREATE TEMP TABLE tmp_test_payments ON COMMIT DROP AS
    SELECT id, journal_entry_id FROM payments
    WHERE tenant_id = v_tenant
      AND bank_account_id IN (SELECT id FROM tmp_test_bank_accounts);

  -- Scoped to the demo tenant at the source. The entry delete below carries
  -- `AND tenant_id = v_tenant` while the LINE delete cannot, so without this
  -- an entry belonging to another tenant would lose its lines and survive as
  -- an empty shell — which the balance check would not catch, since an entry
  -- with no lines sums to 0 = 0.
  CREATE TEMP TABLE tmp_test_jes ON COMMIT DROP AS
    SELECT DISTINCT p.journal_entry_id AS id
    FROM tmp_test_payments p
    JOIN journal_entries je ON je.id = p.journal_entry_id
    WHERE p.journal_entry_id IS NOT NULL AND je.tenant_id = v_tenant;

  SELECT count(*) INTO v_je_count FROM tmp_test_jes;
  RAISE NOTICE '474: % test bank accounts, % payments, % journal entries',
    (SELECT count(*) FROM tmp_test_bank_accounts),
    (SELECT count(*) FROM tmp_test_payments), v_je_count;

  -- Accounts whose incremental current_balance the deleted lines touched;
  -- resynced from the ledger after the deletes.
  CREATE TEMP TABLE tmp_touched_accounts ON COMMIT DROP AS
    SELECT DISTINCT account_id FROM journal_entry_lines
    WHERE journal_entry_id IN (SELECT id FROM tmp_test_jes);

  -- Delete children first (FK order), triggers relaxed for this tx only --
  ALTER TABLE journal_entry_lines DISABLE TRIGGER trg_enforce_journal_line_invariants;

  DELETE FROM payments WHERE id IN (SELECT id FROM tmp_test_payments); -- allocations cascade
  DELETE FROM journal_entry_lines WHERE journal_entry_id IN (SELECT id FROM tmp_test_jes);
  DELETE FROM journal_entries WHERE id IN (SELECT id FROM tmp_test_jes)
    AND tenant_id = v_tenant;
  DELETE FROM bank_transactions
    WHERE tenant_id = v_tenant
      AND bank_account_id IN (SELECT id FROM tmp_test_bank_accounts)
      AND reference LIKE 'TEST-%';

  -- Flush the deferred balance-check trigger now (each fully-deleted entry
  -- sums 0 = 0) — ALTER TABLE refuses while trigger events are pending.
  SET CONSTRAINTS ALL IMMEDIATE;
  ALTER TABLE journal_entry_lines ENABLE TRIGGER trg_enforce_journal_line_invariants;

  UPDATE bank_accounts SET deleted_at = NOW(), is_active = false, updated_at = NOW()
    WHERE id IN (SELECT id FROM tmp_test_bank_accounts);

  -- Resync the denormalized balance of the accounts the deleted lines hit
  UPDATE accounts a SET current_balance = COALESCE(l.bal, 0), updated_at = NOW()
  FROM (
    SELECT ta.account_id,
           SUM(jel.debit_amount - jel.credit_amount) AS bal
    FROM tmp_touched_accounts ta
    LEFT JOIN journal_entry_lines jel ON jel.account_id = ta.account_id
    LEFT JOIN journal_entries je ON je.id = jel.journal_entry_id
      AND je.status = 'posted' AND je.deleted_at IS NULL
    GROUP BY ta.account_id
  ) l
  WHERE a.id = l.account_id;

  -- Other fixture residue (soft-delete: all are FK-referenced or app-soft-deleted)
  UPDATE budgets SET deleted_at = NOW(), updated_at = NOW()
    WHERE tenant_id = v_tenant AND deleted_at IS NULL
      AND name LIKE 'Test Byudjet%';

  UPDATE contacts SET deleted_at = NOW(), is_active = false, updated_at = NOW()
    WHERE tenant_id = v_tenant AND deleted_at IS NULL
      AND name IN ('Test Yetkazuvchi MChJ', 'Test Xaridor MChJ');

  UPDATE procurement_contracts SET deleted_at = NOW(), updated_at = NOW()
    WHERE tenant_id = v_tenant AND deleted_at IS NULL
      AND title LIKE 'Test shartnoma %';

  -- One real, GL-linked settlement account so the Pul oqimi tab has an
  -- honest surface (unique on (tenant_id, account_id) guards the re-run).
  INSERT INTO bank_accounts (tenant_id, organization_id, name, bank_name,
                             account_number, currency, account_type, account_id, is_active)
  SELECT v_tenant, a.organization_id, 'Hisob-kitob schyoti', 'NBU',
         '20208000900000000001', 'UZS', 'checking', a.id, true
  FROM accounts a
  WHERE a.tenant_id = v_tenant AND a.code = '5110'
    AND a.is_leaf = true AND a.is_active = true AND a.deleted_at IS NULL
  ORDER BY a.created_at LIMIT 1
  ON CONFLICT (tenant_id, account_id) DO NOTHING;

  -- Assertions ----------------------------------------------------------
  SELECT COALESCE(SUM(jel.debit_amount), 0), COALESCE(SUM(jel.credit_amount), 0)
    INTO v_dr, v_cr
  FROM journal_entry_lines jel
  JOIN journal_entries je ON je.id = jel.journal_entry_id
  WHERE je.tenant_id = v_tenant AND je.status = 'posted' AND je.deleted_at IS NULL;
  -- The property that actually belongs to this migration: it changed nothing
  -- about the books. Entries and their lines are deleted together, so the
  -- difference must come out exactly as it went in. If it moved, the delete
  -- caught something it should not have — lines without their entry, or an
  -- entry belonging to another tenant — and the transaction must roll back.
  IF (v_dr - v_cr) <> (v_dr_before - v_cr_before) THEN
    RAISE EXCEPTION '474: cleanup changed the demo trial balance: was Dr % <> Cr % (%), now Dr % <> Cr % (%)',
      v_dr_before, v_cr_before, v_dr_before - v_cr_before,
      v_dr, v_cr, v_dr - v_cr;
  END IF;

  SELECT count(*) INTO v_leftover FROM bank_accounts
  WHERE tenant_id = v_tenant AND deleted_at IS NULL AND name = 'Test Bank UZS';
  IF v_leftover > 0 THEN
    RAISE EXCEPTION '474: % active Test Bank UZS accounts survived cleanup', v_leftover;
  END IF;

  RAISE NOTICE '474: cleanup complete, trial balance Dr % / Cr % (unchanged by this migration)', v_dr, v_cr;
END
$cleanup$;
