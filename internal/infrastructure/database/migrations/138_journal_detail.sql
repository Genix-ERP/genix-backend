-- Fix existing broken columns (entity expects them but DB doesn't have them)
ALTER TABLE journals ADD COLUMN IF NOT EXISTS description TEXT DEFAULT '';
ALTER TABLE journals ADD COLUMN IF NOT EXISTS auto_sequence BOOLEAN DEFAULT true;
ALTER TABLE journals ADD COLUMN IF NOT EXISTS number_prefix VARCHAR(20) DEFAULT '';
ALTER TABLE journals ADD COLUMN IF NOT EXISTS next_number INTEGER DEFAULT 1;
ALTER TABLE journals ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP DEFAULT NOW();

-- New Odoo-style fields
ALTER TABLE journals ADD COLUMN IF NOT EXISTS short_code VARCHAR(10) DEFAULT '';
ALTER TABLE journals ADD COLUMN IF NOT EXISTS currency VARCHAR(10) DEFAULT '';
ALTER TABLE journals ADD COLUMN IF NOT EXISTS bank_account_id UUID REFERENCES bank_accounts(id);
ALTER TABLE journals ADD COLUMN IF NOT EXISTS suspense_account_id UUID REFERENCES accounts(id);
ALTER TABLE journals ADD COLUMN IF NOT EXISTS profit_account_id UUID REFERENCES accounts(id);
ALTER TABLE journals ADD COLUMN IF NOT EXISTS loss_account_id UUID REFERENCES accounts(id);
ALTER TABLE journals ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);

-- Backfill short_code from code (first 5 chars)
UPDATE journals SET short_code = LEFT(code, 5) WHERE short_code = '' OR short_code IS NULL;

-- Journal-Payment Methods linking table (Odoo Incoming/Outgoing Payments tabs)
CREATE TABLE IF NOT EXISTS journal_payment_methods (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    journal_id UUID NOT NULL REFERENCES journals(id) ON DELETE CASCADE,
    payment_method_id UUID NOT NULL REFERENCES payment_methods(id),
    direction VARCHAR(20) NOT NULL CHECK (direction IN ('inbound', 'outbound')),
    name VARCHAR(100) DEFAULT '',
    outstanding_account_id UUID REFERENCES accounts(id),
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(journal_id, payment_method_id, direction)
);
