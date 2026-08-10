-- 478: Single period system (docs/moliya-v2/audit.md §5).
--
-- RENUMBERED 478 -> 483 during the dev merge. The original number was already
-- taken on main, and schema_migrations.version is a PRIMARY KEY: two files at
-- one version both enter `pending`, both apply, and the second INSERT violates
-- the key — RunMigrations then fails and the API crash-loops on boot. On a
-- database that already recorded that version the loser is skipped forever
-- instead, and its schema change silently never lands.
--
-- Three period systems existed and none was operational: fiscal_years →
-- fiscal_periods (0 period rows DB-wide), accounting_periods (0 rows DB-wide)
-- and period_closings (the only real close engine, fn_close_accounting_period,
-- with no UI). The survivor is fiscal_years → fiscal_periods: it has the year
-- hierarchy, the open/locked/closed state machine and first position in both
-- enforcement layers (checkPeriodLock + fn_enforce_journal_line_invariants).
--
-- This migration:
--   1. adds the missing uniqueness guard on (fiscal_year_id, period_number);
--   2. backfills rows from accounting_periods (a no-op today — both tables are
--      empty — but written defensively for installs that diverged);
--   3. backfills 12 monthly periods for every fiscal year that has none, so
--      period locking actually has rows to act on;
--   4. extends fn_enforce_journal_entry_status so the draft→posted transition
--      is period-guarded at the DB layer too (previously only one app-level
--      call in PostJournalEntry guarded it), with a fiscal_years.status
--      fallback so "yilni yopish" finally blocks postings.

-- 1. Uniqueness -----------------------------------------------------------
CREATE UNIQUE INDEX IF NOT EXISTS uq_fiscal_periods_year_number
  ON fiscal_periods (fiscal_year_id, period_number);

-- 2. accounting_periods → fiscal_periods (defensive; empty today) ---------
INSERT INTO fiscal_periods (id, fiscal_year_id, code, name, period_number,
                            start_date, end_date, status, locked_by, locked_at,
                            created_at, updated_at)
SELECT uuid_generate_v4(), fy.id,
       to_char(ap.start_date, 'YYYY-MM'),
       to_char(ap.start_date, 'YYYY "yil" MM "oy"'),
       EXTRACT(MONTH FROM ap.start_date)::int,
       ap.start_date, ap.end_date,
       CASE WHEN ap.is_locked THEN 'locked' ELSE 'open' END,
       ap.locked_by, ap.locked_at, NOW(), NOW()
FROM accounting_periods ap
JOIN fiscal_years fy ON fy.tenant_id = ap.tenant_id
  AND ap.start_date >= fy.start_date AND ap.end_date <= fy.end_date
WHERE NOT EXISTS (
  SELECT 1 FROM fiscal_periods fp
  WHERE fp.fiscal_year_id = fy.id
    AND fp.start_date <= ap.start_date AND fp.end_date >= ap.start_date
)
ON CONFLICT (fiscal_year_id, period_number) DO NOTHING;

-- 3. Monthly periods for years that have none ------------------------------
INSERT INTO fiscal_periods (id, fiscal_year_id, code, name, period_number,
                            start_date, end_date, status, created_at, updated_at)
SELECT uuid_generate_v4(), fy.id,
       to_char(m.month_start, 'YYYY-MM'),
       to_char(m.month_start, 'TMMon YYYY'),
       row_number() OVER (PARTITION BY fy.id ORDER BY m.month_start)::int,
       m.month_start::date,
       LEAST((m.month_start + interval '1 month' - interval '1 day')::date, fy.end_date),
       CASE WHEN fy.status = 'closed' THEN 'closed' ELSE 'open' END,
       NOW(), NOW()
FROM fiscal_years fy
CROSS JOIN LATERAL generate_series(
  date_trunc('month', fy.start_date),
  date_trunc('month', fy.end_date),
  interval '1 month') AS m(month_start)
WHERE NOT EXISTS (SELECT 1 FROM fiscal_periods fp WHERE fp.fiscal_year_id = fy.id)
ON CONFLICT (fiscal_year_id, period_number) DO NOTHING;

-- 4. DB-level period guard on posting --------------------------------------
CREATE OR REPLACE FUNCTION fn_enforce_journal_entry_status()
RETURNS TRIGGER AS $$
DECLARE
    v_period_status TEXT;
    v_year_status   TEXT;
BEGIN
    -- Allow setting is_reversal / reversal_of_id (storno workflow creates a new entry)
    -- but disallow flipping posted → draft on the same row, which would undo the audit trail.
    IF OLD.status = 'posted' AND NEW.status IN ('draft', 'cancelled') AND NEW.is_reversal = false THEN
        -- Only allow cancelled if it was reversed (reversed_entry_id populated)
        IF NEW.status = 'cancelled' AND NEW.reversed_entry_id IS NOT NULL THEN
            RETURN NEW;
        END IF;
        RAISE EXCEPTION
            'TT 4.4: posted entries cannot be reverted to draft/cancelled without a storno'
            USING ERRCODE = '23514';
    END IF;

    -- Draft → posted must land in an open period (migration 478). The line
    -- trigger cannot catch this transition (it is a header-only UPDATE), so
    -- the period guard lives here: fiscal_periods first, and when no period
    -- row covers the date, the year status is the fallback — closing a year
    -- with no child periods still blocks postings.
    IF NEW.status = 'posted' AND OLD.status IS DISTINCT FROM 'posted' THEN
        SELECT fp.status INTO v_period_status
        FROM fiscal_periods fp
        JOIN fiscal_years fy ON fy.id = fp.fiscal_year_id
        WHERE fy.tenant_id = NEW.tenant_id
          AND NEW.entry_date BETWEEN fp.start_date AND fp.end_date
        ORDER BY fp.start_date DESC
        LIMIT 1;

        IF v_period_status IN ('locked', 'closed') THEN
            RAISE EXCEPTION
                'Davr yopilgan yoki qulflangan (%) — yozuvni o''tkazib bo''lmaydi',
                to_char(NEW.entry_date, 'YYYY-MM')
                USING ERRCODE = '23514';
        END IF;

        IF v_period_status IS NULL THEN
            SELECT fy.status INTO v_year_status
            FROM fiscal_years fy
            WHERE fy.tenant_id = NEW.tenant_id
              AND NEW.entry_date BETWEEN fy.start_date AND fy.end_date
            ORDER BY fy.start_date DESC
            LIMIT 1;
            IF v_year_status = 'closed' THEN
                RAISE EXCEPTION
                    'Moliyaviy yil yopilgan — yozuvni o''tkazib bo''lmaydi'
                    USING ERRCODE = '23514';
            END IF;
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
