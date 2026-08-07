-- Link a cash transaction to the till it moved money through.
--
-- cash_transactions had no cash_register_id and cash_registers has no
-- account_id, so there was no way in either direction to say which kassa a
-- transaction belonged to. That is why cash transactions never reached
-- cash_book_entries: a cash book row is keyed by (tenant, register, date) and
-- the register was simply unknowable.
--
-- With this column the cash book can be derived from BOTH event sources
-- (cash_orders + cash_transactions) instead of being a materialised table that
-- only ConfirmCashOrder wrote — see kassa_ledger.go. Nullable on purpose: a
-- tenant with several tills has genuinely ambiguous history, and inventing a
-- till for those rows would silently move money into the wrong book.

ALTER TABLE cash_transactions
    ADD COLUMN IF NOT EXISTS cash_register_id UUID REFERENCES cash_registers(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_cash_transactions_register
    ON cash_transactions (tenant_id, cash_register_id, transaction_date);

-- Backfill ONLY where the answer is unambiguous: the tenant has exactly one
-- active till, so every existing transaction must have gone through it. A
-- tenant with two or more tills keeps NULL, because guessing would attribute
-- real money to the wrong kassa and nothing downstream would ever surface it.
UPDATE cash_transactions ct
SET cash_register_id = sole.id
FROM (
    -- (array_agg(...))[1], not MIN(cr.id): Postgres has no min() aggregate for
    -- uuid, and `MIN(cr.id)` fails to PLAN — so it errors even when the table is
    -- empty and no row would ever reach the aggregate. HAVING COUNT(*) = 1
    -- guarantees a single element, so taking the first is exact, not arbitrary.
    SELECT cr.tenant_id, (array_agg(cr.id))[1] AS id
    FROM cash_registers cr
    WHERE cr.deleted_at IS NULL AND cr.is_active = true
    GROUP BY cr.tenant_id
    HAVING COUNT(*) = 1
) sole
WHERE ct.tenant_id = sole.tenant_id
  AND ct.cash_register_id IS NULL;
