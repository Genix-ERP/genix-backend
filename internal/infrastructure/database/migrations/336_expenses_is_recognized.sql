-- 336_expenses_is_recognized.sql
-- Expense recognition flag for profit-tax calculation.
--
-- Uzbekistan SK distinguishes "тан олинган" (recognized) expenses that
-- reduce the profit-tax base from "тан олинмаган" (non-recognized)
-- expenses that appear in the accounting profit but NOT in the tax base.
-- See §6 of ТЗ_Ish_Haqi_Soliq_Tolik.docx.
--
-- Default = TRUE so existing rows keep behaving as before; admin flips
-- individual rows to FALSE for fines, undocumented expenses, personal
-- costs, dividends, etc. (see §6.3 of the TZ for the canonical list).

ALTER TABLE expenses
    ADD COLUMN IF NOT EXISTS is_recognized BOOLEAN NOT NULL DEFAULT TRUE;

-- Most profit-tax queries filter WHERE is_recognized = FALSE to surface the
-- "unrecognized" list; partial index keeps that query hot without bloating
-- the index on the far-more-common recognized rows.
CREATE INDEX IF NOT EXISTS idx_expenses_unrecognized
    ON expenses (tenant_id, expense_date)
    WHERE is_recognized = FALSE AND deleted_at IS NULL;
