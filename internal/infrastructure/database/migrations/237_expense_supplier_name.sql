-- Add supplier_name field to expense lines (Taminotchi)
ALTER TABLE construction_expense_lines
  ADD COLUMN IF NOT EXISTS supplier_name VARCHAR(255);
