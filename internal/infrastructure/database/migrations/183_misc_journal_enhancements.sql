-- MISC Journal enhancements per TZ spec
-- Adds missing columns to journal_entries, journal_entry_lines, fiscal_periods

-- journal_entries: add tags, cancelled_at, is_reversal, reversal_of_id, reversal_reason
ALTER TABLE journal_entries ADD COLUMN IF NOT EXISTS tags TEXT[];
ALTER TABLE journal_entries ADD COLUMN IF NOT EXISTS cancelled_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE journal_entries ADD COLUMN IF NOT EXISTS is_reversal BOOLEAN DEFAULT false;
ALTER TABLE journal_entries ADD COLUMN IF NOT EXISTS reversal_of_id UUID REFERENCES journal_entries(id);
ALTER TABLE journal_entries ADD COLUMN IF NOT EXISTS reversal_reason TEXT;

-- fiscal_periods: add locked_by, locked_at for period locking audit
ALTER TABLE fiscal_periods ADD COLUMN IF NOT EXISTS locked_by UUID REFERENCES users(id);
ALTER TABLE fiscal_periods ADD COLUMN IF NOT EXISTS locked_at TIMESTAMP WITH TIME ZONE;

-- Fix existing rows where both debit and credit are > 0 (keep the larger, zero out the smaller)
UPDATE journal_entry_lines
SET debit_amount = debit_amount - credit_amount, credit_amount = 0
WHERE debit_amount > 0 AND credit_amount > 0 AND debit_amount >= credit_amount;

UPDATE journal_entry_lines
SET credit_amount = credit_amount - debit_amount, debit_amount = 0
WHERE debit_amount > 0 AND credit_amount > 0 AND credit_amount > debit_amount;

-- journal_entry_lines: CHECK constraint — debit and credit cannot both be > 0
ALTER TABLE journal_entry_lines ADD CONSTRAINT chk_debit_credit
    CHECK (NOT (debit_amount > 0 AND credit_amount > 0));

-- Indexes for new columns
CREATE INDEX IF NOT EXISTS idx_je_reversal_of ON journal_entries(reversal_of_id);
CREATE INDEX IF NOT EXISTS idx_je_is_reversal ON journal_entries(is_reversal) WHERE is_reversal = true;
