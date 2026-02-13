-- Add Odoo-style GL account mappings to product categories
-- Each category can define its own revenue, COGS, inventory, and interim accounts

ALTER TABLE product_categories
  ADD COLUMN IF NOT EXISTS income_account_id UUID REFERENCES accounts(id),
  ADD COLUMN IF NOT EXISTS expense_account_id UUID REFERENCES accounts(id),
  ADD COLUMN IF NOT EXISTS stock_valuation_account_id UUID REFERENCES accounts(id),
  ADD COLUMN IF NOT EXISTS stock_input_account_id UUID REFERENCES accounts(id),
  ADD COLUMN IF NOT EXISTS stock_output_account_id UUID REFERENCES accounts(id);

COMMENT ON COLUMN product_categories.income_account_id IS 'Revenue account for sales (Odoo: Income Account)';
COMMENT ON COLUMN product_categories.expense_account_id IS 'COGS account (Odoo: Expense Account)';
COMMENT ON COLUMN product_categories.stock_valuation_account_id IS 'Inventory asset account (Odoo: Stock Valuation)';
COMMENT ON COLUMN product_categories.stock_input_account_id IS 'Interim receipt account (Odoo: Stock Input)';
COMMENT ON COLUMN product_categories.stock_output_account_id IS 'Interim delivery account (Odoo: Stock Output)';
