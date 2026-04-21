-- Add is_payroll_journal flag to journals table
-- Allows admins to designate any journal (cash, bank, general, etc.)
-- as the journal used for payroll salary payments.
ALTER TABLE journals ADD COLUMN IF NOT EXISTS is_payroll_journal BOOLEAN NOT NULL DEFAULT false;

-- Index for fast lookup when processing payroll payments
CREATE INDEX IF NOT EXISTS idx_journals_payroll ON journals(tenant_id, is_payroll_journal) WHERE is_payroll_journal = true;
