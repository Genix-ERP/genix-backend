-- Backfill: Create default chart of accounts for existing organizations that have none
-- Also assigns legacy NULL-org accounts to the tenant's first organization

-- Step 1: Assign legacy accounts (organization_id IS NULL) to the tenant's oldest active org
UPDATE accounts a
SET organization_id = sub.first_org_id
FROM (
    SELECT DISTINCT ON (o.tenant_id) o.tenant_id, o.id AS first_org_id
    FROM organizations o
    WHERE o.deleted_at IS NULL
    ORDER BY o.tenant_id, o.created_at ASC
) sub
WHERE a.tenant_id = sub.tenant_id
  AND a.organization_id IS NULL
  AND a.deleted_at IS NULL;

-- Step 2: Create default chart of accounts for orgs that still have 0 accounts
-- Uses a DO block to iterate over orgs and insert default accounts

DO $$
DECLARE
    org_record RECORD;
    type_record RECORD;
    type_map JSONB := '{}';
    acc RECORD;
    v_now TIMESTAMP WITH TIME ZONE := NOW();
BEGIN
    -- Build account type map
    FOR type_record IN SELECT id, code FROM account_types LOOP
        type_map := type_map || jsonb_build_object(type_record.code, type_record.id::text);
    END LOOP;

    -- Iterate over organizations with no accounts
    FOR org_record IN
        SELECT o.id AS org_id, o.tenant_id
        FROM organizations o
        WHERE o.deleted_at IS NULL
          AND (SELECT COUNT(*) FROM accounts a WHERE a.organization_id = o.id AND a.deleted_at IS NULL) < 51
    LOOP
        -- Insert default accounts for this org
        FOR acc IN
            SELECT * FROM (VALUES
                ('1000', 'Cash', 'CASH', false, false, true, 'Cash on hand'),
                ('1010', 'Petty Cash', 'CASH', false, false, true, 'Petty cash fund'),
                ('1100', 'Bank Account', 'CASH', true, false, true, 'Main bank account'),
                ('1200', 'Accounts Receivable', 'AR', false, true, true, 'Trade receivables from customers'),
                ('1210', 'Allowance for Doubtful Accounts', 'AR', false, false, false, 'Reserve for bad debts'),
                ('1300', 'Inventory', 'INV', false, true, false, 'Goods held for sale'),
                ('1310', 'Raw Materials', 'INV', false, false, false, 'Raw materials inventory'),
                ('1320', 'Work in Progress', 'INV', false, false, false, 'Work in progress inventory'),
                ('1330', 'Finished Goods', 'INV', false, false, false, 'Finished goods inventory'),
                ('1400', 'Prepaid Expenses', 'OA', false, false, false, 'Prepaid expenses'),
                ('1500', 'Fixed Assets', 'FA', false, false, false, 'Property, plant and equipment'),
                ('1510', 'Accumulated Depreciation', 'FA', false, false, false, 'Accumulated depreciation'),
                ('1600', 'Intangible Assets', 'OA', false, false, false, 'Intangible assets'),
                ('2000', 'Accounts Payable', 'AP', false, true, true, 'Trade payables to suppliers'),
                ('2100', 'Accrued Expenses', 'ST_LIAB', false, false, false, 'Accrued liabilities'),
                ('2110', 'Wages Payable', 'ST_LIAB', false, false, false, 'Wages and salaries payable'),
                ('2120', 'Interest Payable', 'ST_LIAB', false, false, false, 'Interest payable'),
                ('2200', 'Tax Payable', 'ST_LIAB', false, false, false, 'Tax liabilities'),
                ('2210', 'VAT Payable', 'ST_LIAB', false, false, false, 'VAT/Sales tax payable'),
                ('2220', 'Income Tax Payable', 'ST_LIAB', false, false, false, 'Income tax payable'),
                ('2300', 'Unearned Revenue', 'ST_LIAB', false, false, false, 'Deferred revenue'),
                ('2400', 'Short-term Loans', 'ST_LIAB', false, false, true, 'Short-term borrowings'),
                ('2500', 'Long-term Loans', 'LT_LIAB', false, false, true, 'Long-term borrowings'),
                ('3000', 'Owner''s Equity', 'EQUITY', false, false, false, 'Owner''s capital'),
                ('3100', 'Share Capital', 'EQUITY', false, false, false, 'Issued share capital'),
                ('3200', 'Retained Earnings', 'RETAIN', false, false, false, 'Accumulated profits'),
                ('3300', 'Current Year Earnings', 'RETAIN', false, false, false, 'Current period profit/loss'),
                ('3400', 'Dividends', 'EQUITY', false, false, false, 'Dividends declared'),
                ('4000', 'Sales Revenue', 'REVENUE', false, false, false, 'Revenue from sales'),
                ('4100', 'Service Revenue', 'REVENUE', false, false, false, 'Revenue from services'),
                ('4200', 'Product Sales', 'REVENUE', false, false, false, 'Revenue from product sales'),
                ('4900', 'Other Income', 'OTHER_INC', false, false, false, 'Miscellaneous income'),
                ('4910', 'Interest Income', 'OTHER_INC', false, false, false, 'Interest earned'),
                ('4920', 'Foreign Exchange Gain', 'OTHER_INC', false, false, false, 'Gain on foreign exchange'),
                ('5000', 'Cost of Goods Sold', 'COGS', false, false, false, 'Direct cost of goods sold'),
                ('5100', 'Direct Materials', 'COGS', false, false, false, 'Cost of raw materials used'),
                ('5200', 'Direct Labor', 'COGS', false, false, false, 'Direct labor costs'),
                ('5300', 'Manufacturing Overhead', 'COGS', false, false, false, 'Manufacturing overhead'),
                ('6000', 'Salaries & Wages', 'OPEX', false, false, false, 'Employee salaries and wages'),
                ('6100', 'Rent Expense', 'OPEX', false, false, false, 'Rent and lease payments'),
                ('6200', 'Utilities', 'OPEX', false, false, false, 'Electricity, water, gas'),
                ('6300', 'Office Supplies', 'OPEX', false, false, false, 'Office supplies expense'),
                ('6400', 'Insurance Expense', 'OPEX', false, false, false, 'Insurance premiums'),
                ('6500', 'Depreciation Expense', 'OPEX', false, false, false, 'Depreciation of assets'),
                ('6600', 'Advertising & Marketing', 'OPEX', false, false, false, 'Marketing expenses'),
                ('6700', 'Professional Fees', 'OPEX', false, false, false, 'Legal, accounting fees'),
                ('6800', 'Travel & Entertainment', 'OPEX', false, false, false, 'Business travel expenses'),
                ('6900', 'Miscellaneous Expense', 'OPEX', false, false, false, 'Other operating expenses'),
                ('7000', 'Interest Expense', 'OTHER_EXP', false, false, false, 'Interest on borrowings'),
                ('7100', 'Bank Charges', 'OTHER_EXP', false, false, false, 'Bank fees and charges'),
                ('7200', 'Foreign Exchange Loss', 'OTHER_EXP', false, false, false, 'Loss on foreign exchange'),
                ('7900', 'Other Expenses', 'OTHER_EXP', false, false, false, 'Miscellaneous expenses')
            ) AS t(code, name, type_code, is_bank, is_control, is_recon, description)
        LOOP
            INSERT INTO accounts (
                id, tenant_id, organization_id, account_type_id, code, name, description,
                is_bank_account, is_control_account, is_reconcilable,
                current_balance, opening_balance, is_active, created_at, updated_at
            ) VALUES (
                uuid_generate_v4(), org_record.tenant_id, org_record.org_id,
                (type_map->>acc.type_code)::uuid,
                acc.code, acc.name, acc.description,
                acc.is_bank, acc.is_control, acc.is_recon,
                0, 0, true, v_now, v_now
            )
            ON CONFLICT (tenant_id, organization_id, code) DO NOTHING;
        END LOOP;
    END LOOP;
END $$;
