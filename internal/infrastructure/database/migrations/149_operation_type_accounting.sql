-- Migration 149: Add accounting fields to warehouse_operation_types
-- Allows configuring journal, debit/credit accounts per operation type
-- so that journal entries are automatically created when operations complete.

ALTER TABLE warehouse_operation_types
  ADD COLUMN IF NOT EXISTS journal_id UUID REFERENCES journals(id),
  ADD COLUMN IF NOT EXISTS debit_account_id UUID REFERENCES accounts(id),
  ADD COLUMN IF NOT EXISTS credit_account_id UUID REFERENCES accounts(id),
  ADD COLUMN IF NOT EXISTS auto_post_accounting BOOLEAN DEFAULT false;
