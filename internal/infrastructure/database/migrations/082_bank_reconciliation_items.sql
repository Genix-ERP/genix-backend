-- Migration: 082_bank_reconciliation_items.sql
-- Description: Add bank reconciliation items and enhance reconciliation workflow
-- Date: 2026-02-06

-- 1. Bank Reconciliation Items (individual items being reconciled)
CREATE TABLE IF NOT EXISTS bank_reconciliation_items (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    reconciliation_id UUID NOT NULL REFERENCES bank_reconciliations(id) ON DELETE CASCADE,
    bank_transaction_id UUID REFERENCES bank_transactions(id),
    journal_entry_line_id UUID REFERENCES journal_entry_lines(id),
    item_type VARCHAR(20) NOT NULL, -- 'bank' (from statement) or 'book' (from journal)
    transaction_date DATE NOT NULL,
    description TEXT,
    amount DECIMAL(20, 4) NOT NULL,
    is_cleared BOOLEAN DEFAULT false,
    cleared_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 2. Add organization_id to bank_reconciliations if not exists
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_name = 'bank_reconciliations' AND column_name = 'organization_id') THEN
        ALTER TABLE bank_reconciliations ADD COLUMN organization_id UUID REFERENCES organizations(id);
    END IF;
END $$;

-- 3. Add is_reconciled column to bank_transactions if not exists
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_name = 'bank_transactions' AND column_name = 'is_reconciled') THEN
        ALTER TABLE bank_transactions ADD COLUMN is_reconciled BOOLEAN DEFAULT false;
    END IF;
END $$;

-- 4. Add reconciled_date column to bank_transactions if not exists
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_name = 'bank_transactions' AND column_name = 'reconciled_date') THEN
        ALTER TABLE bank_transactions ADD COLUMN reconciled_date DATE;
    END IF;
END $$;

-- 5. Add reconciliation_id to bank_transactions for linking
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_name = 'bank_transactions' AND column_name = 'reconciliation_id') THEN
        ALTER TABLE bank_transactions ADD COLUMN reconciliation_id UUID REFERENCES bank_reconciliations(id);
    END IF;
END $$;

-- 6. Add indexes for performance
CREATE INDEX IF NOT EXISTS idx_bank_reconciliation_items_reconciliation ON bank_reconciliation_items(reconciliation_id);
CREATE INDEX IF NOT EXISTS idx_bank_reconciliation_items_bank_tx ON bank_reconciliation_items(bank_transaction_id);
CREATE INDEX IF NOT EXISTS idx_bank_reconciliation_items_je_line ON bank_reconciliation_items(journal_entry_line_id);
CREATE INDEX IF NOT EXISTS idx_bank_transactions_reconciliation ON bank_transactions(reconciliation_id);
CREATE INDEX IF NOT EXISTS idx_bank_transactions_is_reconciled ON bank_transactions(is_reconciled);

-- 7. Add unique constraint on permissions.code for ON CONFLICT support
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'permissions_code_key') THEN
        -- Remove any duplicate codes first (keep the one with lowest id)
        DELETE FROM permissions p1
        WHERE p1.code IS NOT NULL
        AND EXISTS (
            SELECT 1 FROM permissions p2
            WHERE p2.code = p1.code AND p2.id < p1.id
        );
        -- Add unique constraint
        ALTER TABLE permissions ADD CONSTRAINT permissions_code_key UNIQUE (code);
    END IF;
END $$;

-- 8. Add permissions for bank reconciliation
INSERT INTO permissions (code, name, description, module, resource, action)
VALUES
    ('bank_reconciliation:create', 'Create Bank Reconciliation', 'Can create bank reconciliation sessions', 'finance', 'bank_reconciliation', 'create'),
    ('bank_reconciliation:read', 'View Bank Reconciliation', 'Can view bank reconciliation', 'finance', 'bank_reconciliation', 'read'),
    ('bank_reconciliation:update', 'Update Bank Reconciliation', 'Can update bank reconciliation', 'finance', 'bank_reconciliation', 'update'),
    ('bank_reconciliation:delete', 'Delete Bank Reconciliation', 'Can delete bank reconciliation', 'finance', 'bank_reconciliation', 'delete'),
    ('bank_reconciliation:complete', 'Complete Bank Reconciliation', 'Can complete/finalize bank reconciliation', 'finance', 'bank_reconciliation', 'complete')
ON CONFLICT (code) DO NOTHING;
