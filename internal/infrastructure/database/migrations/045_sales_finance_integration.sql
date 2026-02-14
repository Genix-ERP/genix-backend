-- Migration: Sales Finance Integration
-- Creates default accounts and journals for sales-to-finance integration
-- Enables automatic GL posting when invoices are created/paid

-- =====================================================
-- ADD MISSING COLUMNS TO JOURNALS TABLE
-- =====================================================

ALTER TABLE journals ADD COLUMN IF NOT EXISTS description TEXT;
ALTER TABLE journals ADD COLUMN IF NOT EXISTS number_prefix VARCHAR(10);
ALTER TABLE journals ADD COLUMN IF NOT EXISTS next_number INT DEFAULT 1;
ALTER TABLE journals ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP;
ALTER TABLE journals ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;

-- =====================================================
-- CREATE DEFAULT JOURNALS FOR EACH TENANT
-- =====================================================

-- Sales Journal (for customer invoices)
INSERT INTO journals (id, tenant_id, code, name, type, description, number_prefix, next_number, is_active, created_at, updated_at)
SELECT
    gen_random_uuid(),
    t.id,
    'SALES',
    'Sales Journal',
    'sales',
    'Journal for recording sales invoices and customer transactions',
    'SJ',
    1,
    true,
    NOW(),
    NOW()
FROM tenants t
WHERE NOT EXISTS (
    SELECT 1 FROM journals j WHERE j.tenant_id = t.id AND j.code = 'SALES'
);

-- Cash Receipts Journal (for customer payments)
INSERT INTO journals (id, tenant_id, code, name, type, description, number_prefix, next_number, is_active, created_at, updated_at)
SELECT
    gen_random_uuid(),
    t.id,
    'CASH_RECEIPTS',
    'Cash Receipts Journal',
    'cash',
    'Journal for recording customer payments and cash receipts',
    'CR',
    1,
    true,
    NOW(),
    NOW()
FROM tenants t
WHERE NOT EXISTS (
    SELECT 1 FROM journals j WHERE j.tenant_id = t.id AND j.code = 'CASH_RECEIPTS'
);

-- General Journal (for adjustments)
INSERT INTO journals (id, tenant_id, code, name, type, description, number_prefix, next_number, is_active, created_at, updated_at)
SELECT
    gen_random_uuid(),
    t.id,
    'GENERAL',
    'General Journal',
    'general',
    'General journal for adjustments and miscellaneous entries',
    'GJ',
    1,
    true,
    NOW(),
    NOW()
FROM tenants t
WHERE NOT EXISTS (
    SELECT 1 FROM journals j WHERE j.tenant_id = t.id AND j.code = 'GENERAL'
);

-- =====================================================
-- CREATE DEFAULT ACCOUNTS FOR EACH TENANT
-- =====================================================

-- Get account type IDs (these are global, not per tenant)
DO $$
DECLARE
    ar_type_id UUID;
    cash_type_id UUID;
    revenue_type_id UUID;
    tax_type_id UUID;
    cogs_type_id UUID;
    inv_type_id UUID;
    t_record RECORD;
BEGIN
    -- Get account type IDs
    SELECT id INTO ar_type_id FROM account_types WHERE code = 'AR';
    SELECT id INTO cash_type_id FROM account_types WHERE code = 'CASH';
    SELECT id INTO revenue_type_id FROM account_types WHERE code = 'REVENUE';
    SELECT id INTO tax_type_id FROM account_types WHERE code = 'ST_LIAB'; -- Use short-term liability for tax payable
    SELECT id INTO cogs_type_id FROM account_types WHERE code = 'COGS';
    SELECT id INTO inv_type_id FROM account_types WHERE code = 'INV';

    -- Create default accounts for each tenant
    FOR t_record IN SELECT id FROM tenants LOOP
        -- Accounts Receivable (1100)
        INSERT INTO accounts (id, tenant_id, account_type_id, code, name, description, is_active, is_reconcilable, created_at, updated_at)
        SELECT gen_random_uuid(), t_record.id, ar_type_id, '1100', 'Accounts Receivable', 'Trade receivables from customers', true, true, NOW(), NOW()
        WHERE ar_type_id IS NOT NULL AND NOT EXISTS (
            SELECT 1 FROM accounts WHERE tenant_id = t_record.id AND code = '1100'
        );

        -- Cash (1000)
        INSERT INTO accounts (id, tenant_id, account_type_id, code, name, description, is_active, created_at, updated_at)
        SELECT gen_random_uuid(), t_record.id, cash_type_id, '1000', 'Cash', 'Cash on hand', true, NOW(), NOW()
        WHERE cash_type_id IS NOT NULL AND NOT EXISTS (
            SELECT 1 FROM accounts WHERE tenant_id = t_record.id AND code = '1000'
        );

        -- Bank Account (1010)
        INSERT INTO accounts (id, tenant_id, account_type_id, code, name, description, is_active, is_bank_account, is_reconcilable, created_at, updated_at)
        SELECT gen_random_uuid(), t_record.id, cash_type_id, '1010', 'Bank Account', 'Primary bank account', true, true, true, NOW(), NOW()
        WHERE cash_type_id IS NOT NULL AND NOT EXISTS (
            SELECT 1 FROM accounts WHERE tenant_id = t_record.id AND code = '1010'
        );

        -- Sales Revenue (4000)
        INSERT INTO accounts (id, tenant_id, account_type_id, code, name, description, is_active, created_at, updated_at)
        SELECT gen_random_uuid(), t_record.id, revenue_type_id, '4000', 'Sales Revenue', 'Revenue from sales of goods and services', true, NOW(), NOW()
        WHERE revenue_type_id IS NOT NULL AND NOT EXISTS (
            SELECT 1 FROM accounts WHERE tenant_id = t_record.id AND code = '4000'
        );

        -- Sales Tax Payable (2100)
        INSERT INTO accounts (id, tenant_id, account_type_id, code, name, description, is_active, created_at, updated_at)
        SELECT gen_random_uuid(), t_record.id, tax_type_id, '2100', 'Sales Tax Payable', 'Tax collected on sales, payable to tax authority', true, NOW(), NOW()
        WHERE tax_type_id IS NOT NULL AND NOT EXISTS (
            SELECT 1 FROM accounts WHERE tenant_id = t_record.id AND code = '2100'
        );

        -- Cost of Goods Sold (5000)
        INSERT INTO accounts (id, tenant_id, account_type_id, code, name, description, is_active, created_at, updated_at)
        SELECT gen_random_uuid(), t_record.id, cogs_type_id, '5000', 'Cost of Goods Sold', 'Direct costs of goods sold', true, NOW(), NOW()
        WHERE cogs_type_id IS NOT NULL AND NOT EXISTS (
            SELECT 1 FROM accounts WHERE tenant_id = t_record.id AND code = '5000'
        );

        -- Inventory (1200)
        INSERT INTO accounts (id, tenant_id, account_type_id, code, name, description, is_active, created_at, updated_at)
        SELECT gen_random_uuid(), t_record.id, inv_type_id, '1200', 'Inventory', 'Inventory of goods for sale', true, NOW(), NOW()
        WHERE inv_type_id IS NOT NULL AND NOT EXISTS (
            SELECT 1 FROM accounts WHERE tenant_id = t_record.id AND code = '1200'
        );
    END LOOP;
END $$;

-- =====================================================
-- ADD CUSTOMER NAME TO SALES INVOICES FOR EASIER DISPLAY
-- =====================================================

ALTER TABLE sales_invoices ADD COLUMN IF NOT EXISTS customer_name VARCHAR(255);

-- Update existing invoices with customer name
UPDATE sales_invoices si
SET customer_name = c.name
FROM contacts c
WHERE si.customer_id = c.id AND si.customer_name IS NULL;

-- =====================================================
-- INDEX FOR FASTER LOOKUPS
-- =====================================================

CREATE INDEX IF NOT EXISTS idx_accounts_tenant_code ON accounts(tenant_id, code);
CREATE INDEX IF NOT EXISTS idx_journals_tenant_code ON journals(tenant_id, code);
CREATE INDEX IF NOT EXISTS idx_journal_entries_source ON journal_entries(source_type, source_id);
