-- Add default receivable/payable account fields to contacts (Odoo-style partner-account linking)
-- Each contact can have a specific AR/AP account; falls back to tenant default if NULL
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS default_receivable_account_id UUID REFERENCES accounts(id) ON DELETE SET NULL;
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS default_payable_account_id UUID REFERENCES accounts(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_contacts_receivable_account ON contacts(default_receivable_account_id);
CREATE INDEX IF NOT EXISTS idx_contacts_payable_account ON contacts(default_payable_account_id);
