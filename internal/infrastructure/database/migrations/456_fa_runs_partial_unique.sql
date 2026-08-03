-- 456_fa_runs_partial_unique.sql
--
-- Bug caught by the rebuilt integration suite: 437 promised "reversed rows
-- free the key so the period can be re-run", and implemented that for
-- fa_depreciation_entries (partial unique) — but fa_depreciation_runs kept a
-- FULL UNIQUE (tenant_id, period). A reversed run therefore still occupied the
-- period and every re-run failed with SAVE_FAILED. Make the run key partial:
-- at most one LIVE (draft|posted) run per period; reversed runs remain as
-- audit history.
ALTER TABLE fa_depreciation_runs DROP CONSTRAINT IF EXISTS fa_depreciation_runs_tenant_id_period_key;
CREATE UNIQUE INDEX IF NOT EXISTS uq_fa_runs_tenant_period_live
    ON fa_depreciation_runs (tenant_id, period)
    WHERE status <> 'reversed';
