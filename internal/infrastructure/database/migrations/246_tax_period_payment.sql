-- Add payment tracking columns to tax_report_periods
ALTER TABLE tax_report_periods ADD COLUMN IF NOT EXISTS paid_at TIMESTAMPTZ;
ALTER TABLE tax_report_periods ADD COLUMN IF NOT EXISTS paid_by UUID REFERENCES users(id);
ALTER TABLE tax_report_periods ADD COLUMN IF NOT EXISTS payment_journal_entry_id UUID REFERENCES journal_entries(id);
