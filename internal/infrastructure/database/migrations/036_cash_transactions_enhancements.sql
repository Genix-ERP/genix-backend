-- Enhance cash_transactions table for frontend compatibility
-- The cash_transactions table already exists from 003_module_enhancements.sql

-- Add category column if not exists
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'cash_transactions' AND column_name = 'category') THEN
        ALTER TABLE cash_transactions ADD COLUMN category VARCHAR(100);
    END IF;
END $$;

-- Add cashier column if not exists
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'cash_transactions' AND column_name = 'cashier') THEN
        ALTER TABLE cash_transactions ADD COLUMN cashier VARCHAR(255);
    END IF;
END $$;

-- Add currency column as varchar if not exists (frontend sends currency code like 'UZS')
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'cash_transactions' AND column_name = 'currency') THEN
        ALTER TABLE cash_transactions ADD COLUMN currency VARCHAR(10) DEFAULT 'UZS';
    END IF;
END $$;

-- Make account_id optional since frontend doesn't require it
ALTER TABLE cash_transactions ALTER COLUMN account_id DROP NOT NULL;

-- Create index for category queries
CREATE INDEX IF NOT EXISTS idx_cash_transactions_category ON cash_transactions(category);
CREATE INDEX IF NOT EXISTS idx_cash_transactions_type ON cash_transactions(transaction_type);

-- Add cash_transaction permissions to permissions table
INSERT INTO permissions (id, module, resource, action, description)
VALUES
    (gen_random_uuid(), 'finance', 'cash_transaction', 'read', 'View cash transactions'),
    (gen_random_uuid(), 'finance', 'cash_transaction', 'create', 'Create cash transactions'),
    (gen_random_uuid(), 'finance', 'cash_transaction', 'update', 'Update cash transactions'),
    (gen_random_uuid(), 'finance', 'cash_transaction', 'delete', 'Delete cash transactions')
ON CONFLICT DO NOTHING;
