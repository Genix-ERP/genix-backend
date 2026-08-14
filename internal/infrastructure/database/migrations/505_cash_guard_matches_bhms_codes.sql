-- 505: make the negative-cash guard match the chart we actually use.
--
-- Migration 192 installed trg_check_cash_bank_balance to stop a kassa or bank
-- account from going below zero. It matches codes '1000%', '1010%', '1100%' —
-- the old US-style chart. Every tenant now runs the BHMS chart, where cash is
-- 50xx and bank is 51xx, so the trigger has been matching nothing at all: the
-- only thing standing between a short kassa and a negative balance was the
-- per-handler check, and three money-out paths were missing it (employee loan
-- disbursement, dividend payout, sales-return cash refund — all fixed in the
-- same release as this migration).
--
-- The legacy codes are DROPPED rather than kept alongside: in the BHMS chart
-- 1000 is "Tovar-moddiy zaxiralar" and 1010 is "Xom ashyo va materiallar" —
-- inventory, not cash. Guarding them as cash blocked ordinary consumption
-- postings: issuing raw material credits 1010, and the old trigger refused it
-- outright, which aborted the surrounding transaction and killed the COGS
-- entry for the delivery ("postInventoryConsumptionJE: commit failed"). Cash
-- and bank are 50xx/51xx here and nowhere else.
--
-- Two deliberate softenings, so restoring the guard cannot brick a live DB:
--
--   1. Only a DECREASE is refused. A tenant that already carries a negative
--      cash balance from before the handler guards existed must still be able
--      to repair it — including by a bulk recompute that lands on a different
--      negative number. Blocking those would make the account unfixable
--      without dropping the trigger.
--
--   2. `genix.balance_guard = 'off'` bypasses it for the current transaction.
--      Maintenance migrations that rebuild balances from the ledger (407, 448,
--      493 and any future one) set it with SET LOCAL; without an escape hatch
--      a legitimately-negative recompute would abort the migration and
--      crash-loop the runner on every boot.
--
-- The message is the same Uzbek sentence the handlers use, so a refusal reads
-- identically wherever it comes from.

CREATE OR REPLACE FUNCTION check_cash_bank_balance()
RETURNS TRIGGER AS $$
BEGIN
    IF COALESCE(current_setting('genix.balance_guard', true), '') = 'off' THEN
        RETURN NEW;
    END IF;

    IF NEW.current_balance < -0.001
       AND NEW.current_balance < OLD.current_balance
       AND (NEW.code LIKE '50%' OR NEW.code LIKE '51%') THEN
        RAISE EXCEPTION '% (%) hisobida mablag'' yetarli emas. Joriy balans: %',
            NEW.name, NEW.code, ROUND(OLD.current_balance::numeric, 2)
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- 192's trigger is BEFORE UPDATE OF current_balance and already points at this
-- function; recreate it anyway so a DB that lost it comes back into line.
DROP TRIGGER IF EXISTS trg_check_cash_bank_balance ON accounts;

CREATE TRIGGER trg_check_cash_bank_balance
    BEFORE UPDATE OF current_balance ON accounts
    FOR EACH ROW
    EXECUTE FUNCTION check_cash_bank_balance();
