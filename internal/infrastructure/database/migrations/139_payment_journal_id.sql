-- Add journal_id to payments so users can select which journal (bank/cash) to use (Odoo-style)
ALTER TABLE payments ADD COLUMN IF NOT EXISTS journal_id UUID REFERENCES journals(id);
