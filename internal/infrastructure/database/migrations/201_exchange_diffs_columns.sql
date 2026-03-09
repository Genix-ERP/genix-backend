-- Ensure exchange_diffs table exists (may not exist if migration 003 was partial)
CREATE TABLE IF NOT EXISTS exchange_diffs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    currency_id UUID NOT NULL REFERENCES currencies(id) ON DELETE RESTRICT,
    amount_uzs DECIMAL(20, 4) NOT NULL,
    diff_type VARCHAR(10) NOT NULL CHECK (diff_type IN ('positive', 'negative')),
    period_start DATE NOT NULL,
    period_end DATE NOT NULL,
    journal_entry_id UUID REFERENCES journal_entries(id) ON DELETE SET NULL,
    description TEXT,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_exchange_diffs_tenant ON exchange_diffs(tenant_id);
CREATE INDEX IF NOT EXISTS idx_exchange_diffs_currency ON exchange_diffs(currency_id);
CREATE INDEX IF NOT EXISTS idx_exchange_diffs_period ON exchange_diffs(tenant_id, period_start, period_end);

-- Add new columns for TZ-compliant currency gains report
ALTER TABLE exchange_diffs ADD COLUMN IF NOT EXISTS document_number VARCHAR(100);
ALTER TABLE exchange_diffs ADD COLUMN IF NOT EXISTS counterparty_id UUID REFERENCES contacts(id) ON DELETE SET NULL;
ALTER TABLE exchange_diffs ADD COLUMN IF NOT EXISTS counterparty_name VARCHAR(255);
ALTER TABLE exchange_diffs ADD COLUMN IF NOT EXISTS foreign_amount DECIMAL(20, 4) DEFAULT 0;
ALTER TABLE exchange_diffs ADD COLUMN IF NOT EXISTS initial_rate DECIMAL(20, 6) DEFAULT 0;
ALTER TABLE exchange_diffs ADD COLUMN IF NOT EXISTS final_rate DECIMAL(20, 6) DEFAULT 0;
