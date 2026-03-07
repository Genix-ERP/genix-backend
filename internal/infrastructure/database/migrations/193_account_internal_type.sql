-- Add internal_type column to accounts for Odoo-like detailed classification
-- Types: asset_receivable, asset_cash, asset_current, asset_non_current, asset_prepayments, asset_fixed,
--        liability_payable, liability_credit_card, liability_current, liability_non_current,
--        equity, equity_unaffected,
--        income, income_other,
--        expense, expense_depreciation, expense_direct_cost,
--        contra_asset_depreciation, contra_asset_allowance,
--        off_balance

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'accounts' AND column_name = 'internal_type'
    ) THEN
        ALTER TABLE accounts ADD COLUMN internal_type VARCHAR(50);
    END IF;
END $$;

-- Backfill internal_type based on account_type code
UPDATE accounts a SET internal_type = CASE
    WHEN at.code = 'CASH' THEN 'asset_cash'
    WHEN at.code = 'AR' THEN 'asset_receivable'
    WHEN at.code = 'INV' THEN 'asset_current'
    WHEN at.code = 'FA' THEN 'asset_fixed'
    WHEN at.code = 'OA' THEN 'asset_non_current'
    WHEN at.code = 'AP' THEN 'liability_payable'
    WHEN at.code = 'ST_LIAB' THEN 'liability_current'
    WHEN at.code = 'LT_LIAB' THEN 'liability_non_current'
    WHEN at.code = 'EQUITY' THEN 'equity'
    WHEN at.code = 'RETAIN' THEN 'equity_unaffected'
    WHEN at.code = 'REVENUE' THEN 'income'
    WHEN at.code = 'COGS' THEN 'expense_direct_cost'
    WHEN at.code = 'OPEX' THEN 'expense'
    WHEN at.code = 'OTHER_INC' THEN 'income_other'
    WHEN at.code = 'OTHER_EXP' THEN 'expense'
    WHEN at.code = 'CONTRA_ASSET' THEN 'contra_asset_depreciation'
    ELSE 'asset_current'
END
FROM account_types at
WHERE a.account_type_id = at.id AND a.internal_type IS NULL;

CREATE INDEX IF NOT EXISTS idx_accounts_internal_type ON accounts(internal_type) WHERE internal_type IS NOT NULL;
