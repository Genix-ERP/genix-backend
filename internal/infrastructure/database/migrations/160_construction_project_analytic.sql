-- =====================================================
-- Migration 160: Construction Project — analytic account + commission fields
-- =====================================================

ALTER TABLE construction_projects
    ADD COLUMN IF NOT EXISTS analytic_account_id UUID REFERENCES accounts(id),
    ADD COLUMN IF NOT EXISTS commission_date DATE,
    ADD COLUMN IF NOT EXISTS fixed_asset_account_id UUID REFERENCES accounts(id),
    ADD COLUMN IF NOT EXISTS commission_journal_entry_id UUID REFERENCES journal_entries(id);
