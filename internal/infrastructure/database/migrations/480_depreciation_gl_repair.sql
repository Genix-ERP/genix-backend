-- 475: Depreciation GL repair (docs/moliya-v2/audit.md §4, D-1/D-2).
--
-- RENUMBERED 475 -> 480 during the dev merge. The original number was already
-- taken on main, and schema_migrations.version is a PRIMARY KEY: two files at
-- one version both enter `pending`, both apply, and the second INSERT violates
-- the key — RunMigrations then fails and the API crash-loops on boot. On a
-- database that already recorded that version the loser is skipped forever
-- instead, and its schema change silently never lands.
--
-- Two seed defects in the demo tenant:
--   1. Run fc450cde (2026-07) was seeded status='posted' WITHOUT a journal
--      entry, then reversed through the API: the storno FA000311 (24.45M)
--      hit the GL with no original to offset, driving the 02xx contra-asset
--      accounts to a net DEBIT. The reversal already flipped the run's
--      entries to 'reversed' and decremented fa_assets.accumulated_depreciation,
--      so removing the orphan storno restores full consistency for that run.
--   2. Runs 9f847a3f (2026-05) and b9616da0 (2026-06) are 'posted' with no
--      journal entry and no entry links: 48.9M of depreciation exists in the
--      asset register but not in the GL. They are set back to 'draft' (their
--      per-asset entries stay 'active' and unlinked, exactly the state
--      postDepreciationRun expects), and the register increment a real post
--      would have made is backed out so a future real post lands cleanly.
--
-- Predicates target the anomalies, not the ids, so the migration is a no-op
-- on healthy databases. ReverseDepreciationRun now refuses runs without a JE
-- (same change set), so this state cannot regrow.

DO $repair$
DECLARE
  v_tenant uuid;
  v_orphans int;
  v_bad_runs int;
BEGIN
  SELECT id INTO v_tenant FROM tenants WHERE code = 'demo';
  IF v_tenant IS NULL THEN
    RAISE NOTICE '475: no demo tenant, nothing to repair';
    RETURN;
  END IF;

  -- 1. Orphan stornos: posted reversal JEs whose run never produced a run JE.
  CREATE TEMP TABLE tmp_orphan_stornos ON COMMIT DROP AS
    SELECT je.id
    FROM journal_entries je
    JOIN fa_depreciation_runs r
      ON r.id::text = je.source_id::text AND r.tenant_id = je.tenant_id
    WHERE je.tenant_id = v_tenant
      AND je.source_type = 'depreciation_reversal'
      AND je.status = 'posted' AND je.deleted_at IS NULL
      AND r.journal_entry_id IS NULL
      AND NOT EXISTS (
        SELECT 1 FROM journal_entries orig
        WHERE orig.tenant_id = je.tenant_id
          AND orig.source_type = 'depreciation_run'
          AND orig.source_id::text = je.source_id::text
          AND orig.status = 'posted' AND orig.deleted_at IS NULL
      );

  SELECT count(*) INTO v_orphans FROM tmp_orphan_stornos;
  RAISE NOTICE '475: % orphan depreciation storno(s)', v_orphans;

  CREATE TEMP TABLE tmp_touched_accounts_475 ON COMMIT DROP AS
    SELECT DISTINCT account_id FROM journal_entry_lines
    WHERE journal_entry_id IN (SELECT id FROM tmp_orphan_stornos);

  ALTER TABLE journal_entry_lines DISABLE TRIGGER trg_enforce_journal_line_invariants;
  DELETE FROM journal_entry_lines
    WHERE journal_entry_id IN (SELECT id FROM tmp_orphan_stornos);
  UPDATE fa_depreciation_runs
    SET reversal_journal_entry_id = NULL
    WHERE tenant_id = v_tenant
      AND reversal_journal_entry_id IN (SELECT id FROM tmp_orphan_stornos);
  DELETE FROM journal_entries WHERE id IN (SELECT id FROM tmp_orphan_stornos);
  -- Flush the deferred balance-check trigger (fully-deleted entries sum 0 = 0)
  -- before re-enabling — ALTER TABLE refuses while trigger events are pending.
  SET CONSTRAINTS ALL IMMEDIATE;
  ALTER TABLE journal_entry_lines ENABLE TRIGGER trg_enforce_journal_line_invariants;

  UPDATE accounts a SET current_balance = COALESCE(l.bal, 0), updated_at = NOW()
  FROM (
    SELECT ta.account_id,
           SUM(jel.debit_amount - jel.credit_amount) AS bal
    FROM tmp_touched_accounts_475 ta
    LEFT JOIN journal_entry_lines jel ON jel.account_id = ta.account_id
    LEFT JOIN journal_entries je ON je.id = jel.journal_entry_id
      AND je.status = 'posted' AND je.deleted_at IS NULL
    GROUP BY ta.account_id
  ) l
  WHERE a.id = l.account_id;

  -- 2. 'posted' runs with no JE: back to draft, register increment backed out.
  CREATE TEMP TABLE tmp_ghost_runs ON COMMIT DROP AS
    SELECT id FROM fa_depreciation_runs
    WHERE tenant_id = v_tenant AND status = 'posted' AND journal_entry_id IS NULL;

  UPDATE fa_assets fa
    SET accumulated_depreciation = fa.accumulated_depreciation - e.amt,
        updated_at = NOW()
  FROM (
    SELECT asset_id, SUM(amount) AS amt
    FROM fa_depreciation_entries
    WHERE run_id IN (SELECT id FROM tmp_ghost_runs) AND status = 'active'
    GROUP BY asset_id
  ) e
  WHERE fa.id = e.asset_id;

  UPDATE fa_depreciation_runs
    SET status = 'draft', posted_at = NULL, posted_by = NULL
    WHERE id IN (SELECT id FROM tmp_ghost_runs);

  -- Assertions ----------------------------------------------------------
  SELECT count(*) INTO v_orphans
  FROM journal_entries je
  WHERE je.tenant_id = v_tenant
    AND je.source_type = 'depreciation_reversal'
    AND je.status = 'posted' AND je.deleted_at IS NULL
    AND NOT EXISTS (
      SELECT 1 FROM journal_entries orig
      WHERE orig.tenant_id = je.tenant_id
        AND orig.source_type = 'depreciation_run'
        AND orig.source_id::text = je.source_id::text
        AND orig.status = 'posted' AND orig.deleted_at IS NULL
    );
  IF v_orphans > 0 THEN
    RAISE EXCEPTION '475: % orphan depreciation storno(s) survived repair', v_orphans;
  END IF;

  SELECT count(*) INTO v_bad_runs FROM fa_depreciation_runs
  WHERE tenant_id = v_tenant AND status = 'posted' AND journal_entry_id IS NULL;
  IF v_bad_runs > 0 THEN
    RAISE EXCEPTION '475: % posted run(s) without a journal entry survived repair', v_bad_runs;
  END IF;

  RAISE NOTICE '475: depreciation GL repair complete';
END
$repair$;
