-- Add missing columns to bank_accounts table for frontend compatibility
-- The bank_accounts table already exists from 003_module_enhancements.sql

-- Add name column if not exists (frontend uses 'name' for display name)
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'bank_accounts' AND column_name = 'name') THEN
        ALTER TABLE bank_accounts ADD COLUMN name VARCHAR(255);
    END IF;
END $$;

-- Add currency column if not exists
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'bank_accounts' AND column_name = 'currency') THEN
        ALTER TABLE bank_accounts ADD COLUMN currency VARCHAR(10) DEFAULT 'UZS';
    END IF;
END $$;

-- Add account_type column if not exists (checking, savings, money_market, certificate)
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'bank_accounts' AND column_name = 'account_type') THEN
        ALTER TABLE bank_accounts ADD COLUMN account_type VARCHAR(50) DEFAULT 'checking';
    END IF;
END $$;

-- Add balance column if not exists
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'bank_accounts' AND column_name = 'balance') THEN
        ALTER TABLE bank_accounts ADD COLUMN balance DECIMAL(20, 2) DEFAULT 0;
    END IF;
END $$;

-- Add organization_id column if not exists
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'bank_accounts' AND column_name = 'organization_id') THEN
        ALTER TABLE bank_accounts ADD COLUMN organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL;
    END IF;
END $$;

-- Add deleted_at column for soft delete if not exists
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'bank_accounts' AND column_name = 'deleted_at') THEN
        ALTER TABLE bank_accounts ADD COLUMN deleted_at TIMESTAMP;
    END IF;
END $$;

-- Rename last_reconciled_date to last_reconciled if needed
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'bank_accounts' AND column_name = 'last_reconciled_date')
       AND NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'bank_accounts' AND column_name = 'last_reconciled') THEN
        ALTER TABLE bank_accounts RENAME COLUMN last_reconciled_date TO last_reconciled;
    END IF;
END $$;

-- Make account_id optional (nullable) since not all bank accounts may be linked to chart of accounts
ALTER TABLE bank_accounts ALTER COLUMN account_id DROP NOT NULL;

-- Create index on deleted_at for soft delete queries
CREATE INDEX IF NOT EXISTS idx_bank_accounts_deleted ON bank_accounts(tenant_id) WHERE deleted_at IS NULL;

-- Add bank_account permissions to permissions table
INSERT INTO permissions (id, module, resource, action, description)
VALUES
    (gen_random_uuid(), 'finance', 'bank_account', 'read', 'View bank accounts'),
    (gen_random_uuid(), 'finance', 'bank_account', 'create', 'Create bank accounts'),
    (gen_random_uuid(), 'finance', 'bank_account', 'update', 'Update bank accounts'),
    (gen_random_uuid(), 'finance', 'bank_account', 'delete', 'Delete bank accounts')
ON CONFLICT DO NOTHING;
