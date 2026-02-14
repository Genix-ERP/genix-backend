-- Add bank_account_id to payments table to track which account the payment goes through
ALTER TABLE payments ADD COLUMN IF NOT EXISTS bank_account_id UUID REFERENCES accounts(id);
