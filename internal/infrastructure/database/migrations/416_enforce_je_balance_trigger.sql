-- 416_enforce_je_balance_trigger.sql
--
-- Adds a CONSTRAINT TRIGGER that rejects any journal_entry whose lines
-- don't sum to balanced DR/CR. Fires DEFERRED at commit so it can see
-- the full set of lines a transaction inserted, not row-by-row.
--
-- Why this is needed
-- ------------------
-- The existing trigger trg_enforce_journal_line_invariants
-- (fn_enforce_journal_line_invariants) checks per-line invariants —
-- leaf-only postings, period locks, mandatory analytics — but does
-- NOT check that a JE's lines balance overall. That gap let the
-- following classes of bugs slip into production:
--
--   * Goods-delivery JEs where the CR side INSERT silently failed
--     (uuid.Nil account_id) and the Go code didn't check the error.
--     50+ DR-only "Goods Delivery" JEs accumulated this way,
--     permanently inflating asset accounts without inventory CR.
--
--   * Production-order JEs where the labor CR account lookup failed,
--     leaving a DR 2010 line with no matching CR.
--
--   * Sales-return JEs missing their DR 2810 (restock) line.
--
-- After this trigger any transaction that COMMITs an imbalanced JE
-- is REJECTed by Postgres. The error surfaces to the Go handler as
-- a tx.Commit() failure, which the code DOES check (versus the
-- per-Exec errors it has historically ignored).
--
-- Migrations that DELIBERATELY post imbalanced rows in batches
-- (the 411/412/415 reclass family used DISABLE TRIGGER for this)
-- must also disable trg_check_je_balance the same way:
--   ALTER TABLE journal_entry_lines DISABLE TRIGGER trg_check_je_balance;
-- Triggers re-enable automatically at transaction end if the DISABLE
-- was inside a transaction.

CREATE OR REPLACE FUNCTION fn_check_je_balance() RETURNS TRIGGER AS $$
DECLARE
    v_je_id  UUID;
    v_sum_dr NUMERIC;
    v_sum_cr NUMERIC;
BEGIN
    v_je_id := COALESCE(NEW.journal_entry_id, OLD.journal_entry_id);

    SELECT COALESCE(SUM(debit_amount), 0), COALESCE(SUM(credit_amount), 0)
      INTO v_sum_dr, v_sum_cr
    FROM journal_entry_lines
    WHERE journal_entry_id = v_je_id;

    -- Balanced (both zero counts as balanced — JE not yet populated)
    IF v_sum_dr <> v_sum_cr THEN
        RAISE EXCEPTION
            'TT §4.1: journal entry % is imbalanced: SUM(debit)=% SUM(credit)=% (diff=%)',
            v_je_id, v_sum_dr, v_sum_cr, (v_sum_dr - v_sum_cr)
            USING ERRCODE = '23514';
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_check_je_balance ON journal_entry_lines;

CREATE CONSTRAINT TRIGGER trg_check_je_balance
    AFTER INSERT OR UPDATE OR DELETE ON journal_entry_lines
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION fn_check_je_balance();
