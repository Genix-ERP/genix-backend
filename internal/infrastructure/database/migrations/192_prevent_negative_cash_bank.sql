-- Prevent cash (1000*) and bank (1010*, 1100*) accounts from going negative.
-- A database trigger is the safest approach: it catches ALL balance updates regardless of which handler runs them.

CREATE OR REPLACE FUNCTION check_cash_bank_balance()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.current_balance < -0.001 AND (
        NEW.code LIKE '1000%' OR NEW.code LIKE '1010%' OR NEW.code LIKE '1100%'
    ) THEN
        RAISE EXCEPTION '% (%) hisobida mablag'' yetarli emas. Balans: %', NEW.name, NEW.code, ROUND(NEW.current_balance::numeric, 2)
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_check_cash_bank_balance ON accounts;

CREATE TRIGGER trg_check_cash_bank_balance
    BEFORE UPDATE OF current_balance ON accounts
    FOR EACH ROW
    EXECUTE FUNCTION check_cash_bank_balance();
