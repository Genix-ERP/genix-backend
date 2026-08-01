-- ============================================================================
-- GenixERP Demo Seed Data for demo@genixerp.com (Demo Company)
-- ============================================================================
-- Run: psql -d genixerp -f genix-backend/seed_demo.sql
-- ============================================================================
-- Prerequisites: demo@genixerp.com user must already exist (registered via UI)
-- This script uses existing accounts, journals, warehouse created during registration
-- ============================================================================

BEGIN;

-- ============================================================================
-- CLEANUP: Delete existing demo data for re-runnability
-- ============================================================================
DO $$
DECLARE
    v_tid UUID := 'df372dc3-0b77-4ec6-aef3-c1145ecbeaac';
BEGIN
    -- Delete in reverse dependency order
    DELETE FROM payment_allocations WHERE payment_id IN (SELECT id FROM payments WHERE tenant_id = v_tid);
    DELETE FROM payments WHERE tenant_id = v_tid;
    DELETE FROM sales_invoice_lines WHERE sales_invoice_id IN (SELECT id FROM sales_invoices WHERE tenant_id = v_tid);
    DELETE FROM sales_invoices WHERE tenant_id = v_tid;
    DELETE FROM purchase_invoice_lines WHERE purchase_invoice_id IN (SELECT id FROM purchase_invoices WHERE tenant_id = v_tid);
    DELETE FROM purchase_invoices WHERE tenant_id = v_tid;
    DELETE FROM goods_receipt_lines WHERE goods_receipt_id IN (SELECT id FROM goods_receipts WHERE tenant_id = v_tid);
    DELETE FROM goods_receipts WHERE tenant_id = v_tid;
    DELETE FROM sales_delivery_order_lines WHERE delivery_order_id IN (SELECT id FROM sales_delivery_orders WHERE tenant_id = v_tid);
    DELETE FROM sales_delivery_orders WHERE tenant_id = v_tid;
    DELETE FROM sales_order_lines WHERE sales_order_id IN (SELECT id FROM sales_orders WHERE tenant_id = v_tid);
    DELETE FROM sales_orders WHERE tenant_id = v_tid;
    DELETE FROM purchase_order_lines WHERE purchase_order_id IN (SELECT id FROM purchase_orders WHERE tenant_id = v_tid);
    DELETE FROM purchase_orders WHERE tenant_id = v_tid;
    DELETE FROM inventory_transactions WHERE tenant_id = v_tid;
    DELETE FROM inventory WHERE tenant_id = v_tid;
    DELETE FROM fixed_assets WHERE tenant_id = v_tid;
    DELETE FROM journal_entry_lines WHERE journal_entry_id IN (SELECT id FROM journal_entries WHERE tenant_id = v_tid);
    DELETE FROM journal_entries WHERE tenant_id = v_tid;
    DELETE FROM products WHERE tenant_id = v_tid;
    DELETE FROM product_categories WHERE tenant_id = v_tid;
    DELETE FROM workflow_logs WHERE tenant_id = v_tid;
    DELETE FROM workflow_fired_markers WHERE tenant_id = v_tid;
    DELETE FROM workflow_rules WHERE tenant_id = v_tid;
    DELETE FROM bank_accounts WHERE tenant_id = v_tid;
    DELETE FROM payment_methods WHERE tenant_id = v_tid;
    DELETE FROM contacts WHERE tenant_id = v_tid;
    DELETE FROM units_of_measure WHERE tenant_id = v_tid;
    -- Reset account balances
    UPDATE accounts SET current_balance = 0, updated_at = NOW() WHERE tenant_id = v_tid;
    RAISE NOTICE 'Cleanup complete - existing demo data removed.';
END;
$$;

DO $$
DECLARE
    v_tenant_id UUID := 'df372dc3-0b77-4ec6-aef3-c1145ecbeaac';
    v_org_id UUID := '043e7051-6765-4431-92ac-015773fe9e0b';
    v_user_id UUID := '43aa617b-1df4-463e-84cf-baf71a982e20';

    -- Currency IDs (global)
    cur_uzs UUID := '08418f46-8679-48a9-a801-84bede302594';
    cur_usd UUID := '16eb9574-3bf6-4459-b6f1-871ce941491e';

    -- Existing Account IDs (from registration)
    acc_cash UUID := 'bb99e4da-a531-4faf-8e41-fb3b9b829798';       -- 1000 Cash
    acc_petty_cash UUID := 'f86dc4b5-7484-4803-affc-3b8b89da9290'; -- 1010 Petty Cash
    acc_bank UUID := 'af5b5852-e88c-48d4-8ee6-ddeb919b9aa9';       -- 1100 Bank Account
    acc_ar UUID := '0b631f01-c64e-4bb2-bc4b-36c89e5e3a29';         -- 1200 Accounts Receivable
    acc_inv UUID := '107c19e9-2a3a-4625-a9e3-3dbb1023749a';        -- 1300 Inventory
    acc_inv_raw UUID := '0ce35337-80f3-4ae3-bf29-bad372de348e';    -- 1310 Raw Materials
    acc_inv_finished UUID := '6afe789e-faad-4871-9545-f79de527b336'; -- 1330 Finished Goods
    acc_prepaid UUID := 'fcce47c5-9d6f-406c-a6cc-5945deb05ac1';   -- 1400 Prepaid Expenses
    acc_fa UUID := '204f706c-def3-4704-ad33-ab12faf8f62f';         -- 1500 Fixed Assets
    acc_accum_depr UUID := '65b917ba-9da7-4815-a133-a00b0ae68ee7'; -- 1510 Accumulated Depreciation
    acc_ap UUID := '0582ca1b-131b-4907-b661-f8d23d947697';         -- 2000 Accounts Payable
    acc_tax_payable UUID := '90376e72-556f-49c1-8a93-eacd9624c89c'; -- 2200 Tax Payable
    acc_vat_payable UUID := 'cfbd7c1a-f85b-4cd8-9f5b-46138b316dc7'; -- 2210 VAT Payable
    acc_wages_payable UUID := '51885a87-a8d0-4ee0-868b-b4b68263a57c'; -- 2110 Wages Payable
    acc_unearned UUID := '865402d9-4c77-440e-84db-240f58179d3e';   -- 2300 Unearned Revenue
    acc_lt_loan UUID := 'b7f4c871-cc66-4df1-b180-1e2e05f2056c';   -- 2500 Long-term Loans
    acc_equity UUID := '3a65953c-f3fb-463b-aa15-bdf93c2edf65';     -- 3000 Owner's Equity
    acc_share_cap UUID := 'e43db3fd-c2d7-45ea-8130-732fa6af1776';  -- 3100 Share Capital
    acc_retained UUID := '680d54da-626d-4bef-886f-ba7f21708893';   -- 3200 Retained Earnings
    acc_sales_rev UUID := 'de44d136-de1f-4eb7-964d-591c5ca6d8fc';  -- 4000 Sales Revenue
    acc_service_rev UUID := '190c3376-3d50-495c-ad12-f5339e4a5b04'; -- 4100 Service Revenue
    acc_other_inc UUID := 'd452d6d1-c5a8-4cdc-8d85-5ff110a785e6';  -- 4900 Other Income
    acc_cogs UUID := 'f2d3936a-c00e-4932-83b0-c5d2f11eaf5b';       -- 5000 Cost of Goods Sold
    acc_salaries UUID := '964a58c3-d95a-467e-8e5f-35beb03b4d34';   -- 6000 Salaries & Wages
    acc_rent UUID := 'd78df761-a0b5-441c-995a-39f1b3531d39';       -- 6100 Rent Expense
    acc_utilities UUID := '2f2680e9-c0e2-4857-ae2f-39360c8e22d4';  -- 6200 Utilities
    acc_office UUID := '0386124b-427d-4c0b-8f29-8deead2f4ffe';     -- 6300 Office Supplies
    acc_depr_exp UUID := 'ca09739c-831d-451a-bb73-ec8ab036ca6e';   -- 6500 Depreciation Expense
    acc_marketing UUID := '9ca08384-8cf1-4623-b5c4-f4dab196449c';  -- 6600 Advertising & Marketing
    acc_travel UUID := '4f6084de-abb4-4796-81ac-82c81ec9115f';     -- 6800 Travel & Entertainment
    acc_interest_exp UUID := '9be6a48c-a1a6-4935-aec4-81bce56e0c18'; -- 7000 Interest Expense
    acc_bank_fees UUID := 'ca58d4fa-8188-4b57-be40-7c2ce05c7dbd';  -- 7100 Bank Charges

    -- Existing Journal IDs (from registration)
    j_general UUID := '8ff2e00e-2143-4298-9476-4554d1f2e36c';
    j_sales UUID := 'd40caafe-f69a-4b01-845c-fc7630d02750';
    j_purchase UUID := 'b6b8274b-8a12-464f-92cb-e90649ab3b0f';
    j_cash UUID := '230da910-0de8-47df-b5cb-a493901e94a1';
    j_bank UUID := '3fcaad2b-8f89-407e-8588-3e4ee537410a';

    -- Existing Warehouse
    wh_main UUID := '8c6a3192-d543-452e-a711-23b743ca9a78';

    -- Account Type IDs (for product categories)
    at_inv UUID := '0b50d8d2-1a09-4d13-a141-7ff2fdc94bcb';

    -- New UUIDs for seeded data
    -- Contact UUIDs
    ct_customer1 UUID := gen_random_uuid();
    ct_customer2 UUID := gen_random_uuid();
    ct_customer3 UUID := gen_random_uuid();
    ct_customer4 UUID := gen_random_uuid();
    ct_customer5 UUID := gen_random_uuid();
    ct_vendor1 UUID := gen_random_uuid();
    ct_vendor2 UUID := gen_random_uuid();
    ct_vendor3 UUID := gen_random_uuid();
    ct_vendor4 UUID := gen_random_uuid();
    ct_vendor5 UUID := gen_random_uuid();

    -- Product Category UUIDs
    pcat_goods UUID := gen_random_uuid();
    pcat_services UUID := gen_random_uuid();
    pcat_raw UUID := gen_random_uuid();

    -- Unit of Measure UUIDs
    uom_pcs UUID := gen_random_uuid();
    uom_kg UUID := gen_random_uuid();
    uom_hr UUID := gen_random_uuid();
    uom_m UUID := gen_random_uuid();
    uom_l UUID := gen_random_uuid();

    -- Product UUIDs
    prod1 UUID := gen_random_uuid();
    prod2 UUID := gen_random_uuid();
    prod3 UUID := gen_random_uuid();
    prod4 UUID := gen_random_uuid();
    prod5 UUID := gen_random_uuid();
    prod6 UUID := gen_random_uuid();
    prod7 UUID := gen_random_uuid();
    prod8 UUID := gen_random_uuid();

    -- Bank Account UUID
    ba_main UUID := gen_random_uuid();

    -- Payment Method UUIDs
    pm_cash UUID := gen_random_uuid();
    pm_bank UUID := gen_random_uuid();
    pm_card UUID := gen_random_uuid();

    -- Sales Invoice UUIDs
    si1 UUID := gen_random_uuid();
    si2 UUID := gen_random_uuid();
    si3 UUID := gen_random_uuid();
    si4 UUID := gen_random_uuid();
    si5 UUID := gen_random_uuid();
    si6 UUID := gen_random_uuid();

    -- Purchase Invoice UUIDs
    pi1 UUID := gen_random_uuid();
    pi2 UUID := gen_random_uuid();
    pi3 UUID := gen_random_uuid();
    pi4 UUID := gen_random_uuid();
    pi5 UUID := gen_random_uuid();

    -- Payment UUIDs
    pay1 UUID := gen_random_uuid();
    pay2 UUID := gen_random_uuid();
    pay3 UUID := gen_random_uuid();
    pay4 UUID := gen_random_uuid();
    pay5 UUID := gen_random_uuid();
    pay6 UUID := gen_random_uuid();
    pay7 UUID := gen_random_uuid();
    pay8 UUID := gen_random_uuid();

    -- Journal Entry UUIDs
    je1 UUID := gen_random_uuid();
    je2 UUID := gen_random_uuid();
    je3 UUID := gen_random_uuid();
    je4 UUID := gen_random_uuid();
    je5 UUID := gen_random_uuid();
    je6 UUID := gen_random_uuid();
    je7 UUID := gen_random_uuid();
    je8 UUID := gen_random_uuid();
    je9 UUID := gen_random_uuid();
    je10 UUID := gen_random_uuid();
    je11 UUID := gen_random_uuid();
    je12 UUID := gen_random_uuid();
    je13 UUID := gen_random_uuid();
    je14 UUID := gen_random_uuid();
    je15 UUID := gen_random_uuid();
    je16 UUID := gen_random_uuid();

    -- Fixed Asset UUIDs
    fa1 UUID := gen_random_uuid();
    fa2 UUID := gen_random_uuid();
    fa3 UUID := gen_random_uuid();

    -- Sales Order UUIDs
    so1 UUID := gen_random_uuid();
    so2 UUID := gen_random_uuid();
    so3 UUID := gen_random_uuid();
    so4 UUID := gen_random_uuid();
    so5 UUID := gen_random_uuid();
    so6 UUID := gen_random_uuid();

    -- Sales Order Line UUIDs (needed for delivery order lines FK)
    sol1_1 UUID := gen_random_uuid();
    sol1_2 UUID := gen_random_uuid();
    sol2_1 UUID := gen_random_uuid();
    sol2_2 UUID := gen_random_uuid();
    sol3_1 UUID := gen_random_uuid();
    sol4_1 UUID := gen_random_uuid();
    sol5_1 UUID := gen_random_uuid();
    sol6_1 UUID := gen_random_uuid();

    -- Purchase Order UUIDs
    po1 UUID := gen_random_uuid();
    po2 UUID := gen_random_uuid();
    po3 UUID := gen_random_uuid();
    po4 UUID := gen_random_uuid();
    po5 UUID := gen_random_uuid();

    -- Purchase Order Line UUIDs (needed for goods receipt lines FK)
    pol1_1 UUID := gen_random_uuid();
    pol2_1 UUID := gen_random_uuid();
    pol3_1 UUID := gen_random_uuid();
    pol4_1 UUID := gen_random_uuid();
    pol5_1 UUID := gen_random_uuid();

    -- Sales Delivery Order UUIDs
    sdo1 UUID := gen_random_uuid();
    sdo2 UUID := gen_random_uuid();
    sdo3 UUID := gen_random_uuid();
    sdo4 UUID := gen_random_uuid();
    sdo5 UUID := gen_random_uuid();

    -- Goods Receipt UUIDs
    gr1 UUID := gen_random_uuid();
    gr2 UUID := gen_random_uuid();
    gr3 UUID := gen_random_uuid();
    gr4 UUID := gen_random_uuid();
    gr5 UUID := gen_random_uuid();

    -- Warehouse location
    wl_stock UUID := 'fe0cced2-fa46-4682-9407-2c13e5f88ba1';

BEGIN

    -- ============================================================================
    -- 1. UPDATE USER ROLE & PERMISSIONS
    -- ============================================================================
    UPDATE users SET role = 'owner' WHERE id = v_user_id;

    -- Fix warehouse organization_id (registration creates it without org_id)
    UPDATE warehouses SET organization_id = v_org_id WHERE id = wh_main AND organization_id IS NULL;

    -- Update existing Owner role with full permissions
    UPDATE roles SET permissions = '{
        "crm": {"view": true, "create": true, "delete": true, "update": true},
        "hr": {"view": true, "create": true, "delete": true, "update": true},
        "sales": {"view": true, "create": true, "delete": true, "update": true},
        "purchase": {"view": true, "create": true, "delete": true, "update": true},
        "inventory": {"view": true, "create": true, "delete": true, "update": true},
        "financials": {"view": true, "create": true, "delete": true, "update": true},
        "manufacturing": {"view": true, "create": true, "delete": true, "update": true},
        "tasks": {"view": true, "create": true, "delete": true, "update": true},
        "settings": {"view": true, "create": true, "delete": true, "update": true}
    }'::jsonb, updated_at = NOW()
    WHERE tenant_id = v_tenant_id AND code = 'owner';

    -- Grant ALL permissions via role_permissions join table (this is what the app actually checks)
    INSERT INTO role_permissions (role_id, permission_id)
    SELECT r.id, p.id
    FROM roles r, permissions p
    WHERE r.tenant_id = v_tenant_id AND r.code = 'owner'
    ON CONFLICT DO NOTHING;

    -- ============================================================================
    -- 2. TENANT SETTINGS
    -- ============================================================================
    INSERT INTO tenant_settings (tenant_id, settings, updated_at, updated_by, created_at)
    VALUES (v_tenant_id, '{
        "general": {
            "locale": {
                "language": "en",
                "timezone": "Asia/Tashkent",
                "date_format": "DD/MM/YYYY",
                "default_currency": "UZS"
            },
            "company_name": "Demo Company"
        },
        "finance": {
            "fiscal_year_start_month": 1,
            "fiscal_year_start_day": 1,
            "auto_post_journal_entries": false,
            "lock_date": null
        },
        "sales": {
            "default_payment_terms": 30,
            "auto_invoice": false
        },
        "purchase": {
            "default_payment_terms": 30
        },
        "inventory": {
            "default_costing_method": "average",
            "allow_negative_stock": false
        }
    }'::jsonb, NOW(), v_user_id, NOW())
    ON CONFLICT (tenant_id) DO UPDATE SET settings = EXCLUDED.settings, updated_at = NOW();

    -- ============================================================================
    -- 3. UNITS OF MEASURE
    -- ============================================================================
    INSERT INTO units_of_measure (id, tenant_id, code, name, category, is_active, created_at) VALUES
    (uom_pcs, v_tenant_id, 'PCS', 'Pieces',     'unit',   true, NOW()),
    (uom_kg,  v_tenant_id, 'KG',  'Kilograms',  'weight', true, NOW()),
    (uom_hr,  v_tenant_id, 'HR',  'Hours',       'time',   true, NOW()),
    (uom_m,   v_tenant_id, 'M',   'Meters',      'length', true, NOW()),
    (uom_l,   v_tenant_id, 'L',   'Liters',      'volume', true, NOW());

    -- ============================================================================
    -- 4. CONTACTS (5 Customers + 5 Vendors)
    -- ============================================================================
    INSERT INTO contacts (id, tenant_id, organization_id, type, code, name, legal_name, email, phone, payment_terms, credit_limit, current_balance, is_active, created_by, created_at, updated_at) VALUES
    -- Customers
    (ct_customer1, v_tenant_id, v_org_id, 'customer', 'C-001', 'Toshkent Savdo LLC',      'Toshkent Savdo MCHJ',      'info@toshkentsavdo.uz',  '+998901234567', 30, 50000000,  0, true, v_user_id, NOW(), NOW()),
    (ct_customer2, v_tenant_id, v_org_id, 'customer', 'C-002', 'Samarkand Electronics',    'Samarkand Electronics OOO','sales@samelec.uz',       '+998912345678', 15, 30000000,  0, true, v_user_id, NOW(), NOW()),
    (ct_customer3, v_tenant_id, v_org_id, 'customer', 'C-003', 'Bukhara Trading House',    'Bukhara Trading House',    'orders@bukharatrade.uz', '+998933456789', 30, 80000000,  0, true, v_user_id, NOW(), NOW()),
    (ct_customer4, v_tenant_id, v_org_id, 'customer', 'C-004', 'Fergana Valley Agro',      'Fergana Valley Agro MCHJ', 'agro@fvagro.uz',         '+998944567890', 45, 100000000, 0, true, v_user_id, NOW(), NOW()),
    (ct_customer5, v_tenant_id, v_org_id, 'customer', 'C-005', 'Navoi Mining Supply',      'Navoi Mining Supply LLC',  'supply@navoims.uz',      '+998955678901', 30, 150000000, 0, true, v_user_id, NOW(), NOW()),
    -- Vendors
    (ct_vendor1, v_tenant_id, v_org_id, 'vendor', 'V-001', 'China Import Group',    'China Import Group Co.',   'orders@chinaimport.cn', '+998906789012', 30, 0, 0, true, v_user_id, NOW(), NOW()),
    (ct_vendor2, v_tenant_id, v_org_id, 'vendor', 'V-002', 'Korea Tech Supply',     'Korea Tech Supply Ltd.',   'info@koreatech.kr',     '+998917890123', 45, 0, 0, true, v_user_id, NOW(), NOW()),
    (ct_vendor3, v_tenant_id, v_org_id, 'vendor', 'V-003', 'Russian Steel Works',   'Russian Steel Works OOO',  'sales@russteel.ru',     '+998928901234', 30, 0, 0, true, v_user_id, NOW(), NOW()),
    (ct_vendor4, v_tenant_id, v_org_id, 'vendor', 'V-004', 'Turkish Textile Co',    'Turkish Textile Co Ltd.',  'export@turktextile.tr', '+998939012345', 60, 0, 0, true, v_user_id, NOW(), NOW()),
    (ct_vendor5, v_tenant_id, v_org_id, 'vendor', 'V-005', 'Local Office Supplies', 'Local Office Supplies',    'info@localsupply.uz',   '+998940123456', 15, 0, 0, true, v_user_id, NOW(), NOW());

    -- ============================================================================
    -- 5. PAYMENT METHODS
    -- ============================================================================
    INSERT INTO payment_methods (id, tenant_id, code, name, type, account_id, is_active, created_at) VALUES
    (pm_cash, v_tenant_id, 'CASH', 'Cash',          'cash', acc_cash, true, NOW()),
    (pm_bank, v_tenant_id, 'BANK', 'Bank Transfer', 'bank', acc_bank, true, NOW()),
    (pm_card, v_tenant_id, 'CARD', 'Credit Card',   'card', acc_bank, true, NOW());

    -- ============================================================================
    -- 6. BANK ACCOUNT
    -- ============================================================================
    INSERT INTO bank_accounts (id, tenant_id, organization_id, account_id, bank_name, account_number, name, currency, account_type, balance, is_active, created_at, updated_at) VALUES
    (ba_main, v_tenant_id, v_org_id, acc_bank, 'National Bank of Uzbekistan', '20208000123456789012', 'NBU Main Account', 'UZS', 'checking', 0, true, NOW(), NOW());

    -- ============================================================================
    -- 7. PRODUCT CATEGORIES
    -- ============================================================================
    INSERT INTO product_categories (id, tenant_id, organization_id, code, name, description, is_active, income_account_id, expense_account_id, created_at, updated_at) VALUES
    (pcat_goods,    v_tenant_id, v_org_id, 'GOODS',    'Goods',         'Physical goods for sale',      true, acc_sales_rev,   acc_cogs,     NOW(), NOW()),
    (pcat_services, v_tenant_id, v_org_id, 'SERVICES', 'Services',      'Service offerings',            true, acc_service_rev, acc_salaries, NOW(), NOW()),
    (pcat_raw,      v_tenant_id, v_org_id, 'RAW',      'Raw Materials', 'Raw materials for production', true, NULL,            acc_cogs,     NOW(), NOW());

    -- ============================================================================
    -- 8. PRODUCTS (8 products)
    -- ============================================================================
    INSERT INTO products (id, tenant_id, organization_id, category_id, type, code, name, description, unit_id, cost_price, list_price, is_stockable, track_inventory, sales_account_id, purchase_account_id, inventory_account_id, cogs_account_id, is_active, costing_method, created_at, updated_at) VALUES
    (prod1, v_tenant_id, v_org_id, pcat_goods,    'product', 'PRD-001', 'Laptop Computer',       'High-performance laptop',       uom_pcs, 8000000, 12000000, true,  true,  acc_sales_rev, acc_cogs,     acc_inv,     acc_cogs, true, 'average', NOW(), NOW()),
    (prod2, v_tenant_id, v_org_id, pcat_goods,    'product', 'PRD-002', 'Office Desk',           'Standard office desk',          uom_pcs, 1500000, 2500000,  true,  true,  acc_sales_rev, acc_cogs,     acc_inv,     acc_cogs, true, 'average', NOW(), NOW()),
    (prod3, v_tenant_id, v_org_id, pcat_goods,    'product', 'PRD-003', 'Office Chair',          'Ergonomic office chair',        uom_pcs, 2000000, 3500000,  true,  true,  acc_sales_rev, acc_cogs,     acc_inv,     acc_cogs, true, 'average', NOW(), NOW()),
    (prod4, v_tenant_id, v_org_id, pcat_goods,    'product', 'PRD-004', 'Printer',               'Laser printer A4',              uom_pcs, 3000000, 4500000,  true,  true,  acc_sales_rev, acc_cogs,     acc_inv,     acc_cogs, true, 'average', NOW(), NOW()),
    (prod5, v_tenant_id, v_org_id, pcat_goods,    'product', 'PRD-005', 'LED Monitor 27"',       '27 inch LED monitor',           uom_pcs, 2500000, 4000000,  true,  true,  acc_sales_rev, acc_cogs,     acc_inv,     acc_cogs, true, 'average', NOW(), NOW()),
    (prod6, v_tenant_id, v_org_id, pcat_services, 'service', 'SRV-001', 'IT Consulting',         'IT consulting per hour',        uom_hr,  0,       500000,   false, false, acc_service_rev, acc_salaries, NULL,       NULL,     true, 'average', NOW(), NOW()),
    (prod7, v_tenant_id, v_org_id, pcat_services, 'service', 'SRV-002', 'Software Installation', 'OS & software setup',           uom_pcs, 0,       300000,   false, false, acc_service_rev, acc_salaries, NULL,       NULL,     true, 'average', NOW(), NOW()),
    (prod8, v_tenant_id, v_org_id, pcat_raw,      'product', 'RAW-001', 'Steel Sheet',           '1mm steel sheet per kilogram',  uom_kg,  15000,   0,        true,  true,  NULL,          acc_cogs,     acc_inv_raw, acc_cogs, true, 'average', NOW(), NOW());

    -- ============================================================================
    -- 9. INVENTORY (initial stock)
    -- ============================================================================
    INSERT INTO inventory (id, tenant_id, organization_id, product_id, warehouse_id, quantity_on_hand, quantity_reserved, unit_cost, created_at, updated_at) VALUES
    (gen_random_uuid(), v_tenant_id, v_org_id, prod1, wh_main, 25,  0, 8000000, NOW(), NOW()),
    (gen_random_uuid(), v_tenant_id, v_org_id, prod2, wh_main, 40,  0, 1500000, NOW(), NOW()),
    (gen_random_uuid(), v_tenant_id, v_org_id, prod3, wh_main, 50,  0, 2000000, NOW(), NOW()),
    (gen_random_uuid(), v_tenant_id, v_org_id, prod4, wh_main, 15,  0, 3000000, NOW(), NOW()),
    (gen_random_uuid(), v_tenant_id, v_org_id, prod5, wh_main, 30,  0, 2500000, NOW(), NOW()),
    (gen_random_uuid(), v_tenant_id, v_org_id, prod8, wh_main, 500, 0, 15000,   NOW(), NOW());

    -- ============================================================================
    -- 10. SALES ORDERS (6 orders, matching invoices)
    -- ============================================================================

    -- SO-001: Confirmed, fully delivered & invoiced (Toshkent Savdo - Laptops + Monitors)
    INSERT INTO sales_orders (id, tenant_id, organization_id, order_number, customer_id, order_date, expected_date, subtotal, tax_amount, total_amount, status, payment_status, payment_terms, warehouse_id, currency_id, created_by, created_at, updated_at) VALUES
    (so1, v_tenant_id, v_org_id, 'SO-2026-001', ct_customer1, '2026-01-03', '2026-01-10', 36000000, 4320000, 40320000, 'confirmed', 'paid', 30, wh_main, cur_uzs, v_user_id, NOW(), NOW());
    INSERT INTO sales_order_lines (id, sales_order_id, line_number, product_id, description, quantity, unit_price, tax_amount, line_total, quantity_delivered, quantity_invoiced, warehouse_id, created_at, updated_at) VALUES
    (sol1_1, so1, 1, prod1, 'Laptop Computer',  2, 12000000, 2880000, 26880000, 2, 2, wh_main, NOW(), NOW()),
    (sol1_2, so1, 2, prod5, 'LED Monitor 27"',  3, 4000000,  1440000, 13440000, 3, 3, wh_main, NOW(), NOW());

    -- SO-002: Confirmed, fully delivered & invoiced (Samarkand - Chairs + Installation)
    INSERT INTO sales_orders (id, tenant_id, organization_id, order_number, customer_id, order_date, expected_date, subtotal, tax_amount, total_amount, status, payment_status, payment_terms, warehouse_id, currency_id, created_by, created_at, updated_at) VALUES
    (so2, v_tenant_id, v_org_id, 'SO-2026-002', ct_customer2, '2026-01-10', '2026-01-17', 22500000, 2700000, 25200000, 'confirmed', 'partial', 15, wh_main, cur_uzs, v_user_id, NOW(), NOW());
    INSERT INTO sales_order_lines (id, sales_order_id, line_number, product_id, description, quantity, unit_price, tax_amount, line_total, quantity_delivered, quantity_invoiced, warehouse_id, created_at, updated_at) VALUES
    (sol2_1, so2, 1, prod3, 'Office Chair',          5, 3500000, 2100000, 19600000, 5, 5, wh_main, NOW(), NOW()),
    (sol2_2, so2, 2, prod7, 'Software Installation', 4, 300000,  144000,  1344000,  4, 4, NULL,    NOW(), NOW());

    -- SO-003: Confirmed, delivered & invoiced (Bukhara - Desks)
    INSERT INTO sales_orders (id, tenant_id, organization_id, order_number, customer_id, order_date, expected_date, subtotal, tax_amount, total_amount, status, payment_status, payment_terms, warehouse_id, currency_id, created_by, created_at, updated_at) VALUES
    (so3, v_tenant_id, v_org_id, 'SO-2026-003', ct_customer3, '2026-01-18', '2026-01-25', 15000000, 1800000, 16800000, 'confirmed', 'unpaid', 30, wh_main, cur_uzs, v_user_id, NOW(), NOW());
    INSERT INTO sales_order_lines (id, sales_order_id, line_number, product_id, description, quantity, unit_price, tax_amount, line_total, quantity_delivered, quantity_invoiced, warehouse_id, created_at, updated_at) VALUES
    (sol3_1, so3, 1, prod2, 'Office Desk', 6, 2500000, 1800000, 16800000, 6, 6, wh_main, NOW(), NOW());

    -- SO-004: Confirmed, delivered & invoiced (Fergana - Printers)
    INSERT INTO sales_orders (id, tenant_id, organization_id, order_number, customer_id, order_date, expected_date, subtotal, tax_amount, total_amount, status, payment_status, payment_terms, warehouse_id, currency_id, created_by, created_at, updated_at) VALUES
    (so4, v_tenant_id, v_org_id, 'SO-2026-004', ct_customer4, '2026-01-23', '2026-01-30', 9000000, 1080000, 10080000, 'confirmed', 'paid', 45, wh_main, cur_uzs, v_user_id, NOW(), NOW());
    INSERT INTO sales_order_lines (id, sales_order_id, line_number, product_id, description, quantity, unit_price, tax_amount, line_total, quantity_delivered, quantity_invoiced, warehouse_id, created_at, updated_at) VALUES
    (sol4_1, so4, 1, prod4, 'Printer', 2, 4500000, 1080000, 10080000, 2, 2, wh_main, NOW(), NOW());

    -- SO-005: Confirmed, delivered & invoiced (Navoi - Laptops)
    INSERT INTO sales_orders (id, tenant_id, organization_id, order_number, customer_id, order_date, expected_date, subtotal, tax_amount, total_amount, status, payment_status, payment_terms, warehouse_id, currency_id, created_by, created_at, updated_at) VALUES
    (so5, v_tenant_id, v_org_id, 'SO-2026-005', ct_customer5, '2026-01-28', '2026-02-05', 48000000, 5760000, 53760000, 'confirmed', 'unpaid', 30, wh_main, cur_uzs, v_user_id, NOW(), NOW());
    INSERT INTO sales_order_lines (id, sales_order_id, line_number, product_id, description, quantity, unit_price, tax_amount, line_total, quantity_delivered, quantity_invoiced, warehouse_id, created_at, updated_at) VALUES
    (sol5_1, so5, 1, prod1, 'Laptop Computer', 4, 12000000, 5760000, 53760000, 4, 4, wh_main, NOW(), NOW());

    -- SO-006: Confirmed, service order (Toshkent Savdo - IT Consulting)
    INSERT INTO sales_orders (id, tenant_id, organization_id, order_number, customer_id, order_date, expected_date, subtotal, tax_amount, total_amount, status, payment_status, payment_terms, warehouse_id, currency_id, created_by, created_at, updated_at) VALUES
    (so6, v_tenant_id, v_org_id, 'SO-2026-006', ct_customer1, '2026-02-08', '2026-02-15', 10000000, 1200000, 11200000, 'confirmed', 'partial', 30, NULL, cur_uzs, v_user_id, NOW(), NOW());
    INSERT INTO sales_order_lines (id, sales_order_id, line_number, product_id, description, quantity, unit_price, tax_amount, line_total, quantity_delivered, quantity_invoiced, warehouse_id, created_at, updated_at) VALUES
    (sol6_1, so6, 1, prod6, 'IT Consulting', 20, 500000, 1200000, 11200000, 20, 20, NULL, NOW(), NOW());

    -- ============================================================================
    -- 11. PURCHASE ORDERS (5 orders, matching purchase invoices)
    -- ============================================================================

    -- PO-001: Confirmed, received & invoiced (China Import - Laptops)
    INSERT INTO purchase_orders (id, tenant_id, organization_id, order_number, vendor_id, order_date, expected_date, subtotal, tax_amount, total_amount, status, payment_status, payment_terms, warehouse_id, currency_id, created_by, created_at, updated_at) VALUES
    (po1, v_tenant_id, v_org_id, 'PO-2026-001', ct_vendor1, '2025-12-28', '2026-01-05', 80000000, 9600000, 89600000, 'confirmed', 'paid', 30, wh_main, cur_uzs, v_user_id, NOW(), NOW());
    INSERT INTO purchase_order_lines (id, purchase_order_id, line_number, product_id, description, quantity, unit_price, tax_amount, line_total, quantity_received, quantity_invoiced, warehouse_id, created_at, updated_at) VALUES
    (pol1_1, po1, 1, prod1, 'Laptop Computer', 10, 8000000, 9600000, 89600000, 10, 10, wh_main, NOW(), NOW());

    -- PO-002: Confirmed, received & invoiced (Korea Tech - Monitors)
    INSERT INTO purchase_orders (id, tenant_id, organization_id, order_number, vendor_id, order_date, expected_date, subtotal, tax_amount, total_amount, status, payment_status, payment_terms, warehouse_id, currency_id, created_by, created_at, updated_at) VALUES
    (po2, v_tenant_id, v_org_id, 'PO-2026-002', ct_vendor2, '2026-01-05', '2026-01-12', 37500000, 4500000, 42000000, 'confirmed', 'partial', 45, wh_main, cur_uzs, v_user_id, NOW(), NOW());
    INSERT INTO purchase_order_lines (id, purchase_order_id, line_number, product_id, description, quantity, unit_price, tax_amount, line_total, quantity_received, quantity_invoiced, warehouse_id, created_at, updated_at) VALUES
    (pol2_1, po2, 1, prod5, 'LED Monitor 27"', 15, 2500000, 4500000, 42000000, 15, 15, wh_main, NOW(), NOW());

    -- PO-003: Confirmed, received & invoiced (Russian Steel - Steel Sheets)
    INSERT INTO purchase_orders (id, tenant_id, organization_id, order_number, vendor_id, order_date, expected_date, subtotal, tax_amount, total_amount, status, payment_status, payment_terms, warehouse_id, currency_id, created_by, created_at, updated_at) VALUES
    (po3, v_tenant_id, v_org_id, 'PO-2026-003', ct_vendor3, '2026-01-14', '2026-01-20', 7500000, 900000, 8400000, 'confirmed', 'unpaid', 30, wh_main, cur_uzs, v_user_id, NOW(), NOW());
    INSERT INTO purchase_order_lines (id, purchase_order_id, line_number, product_id, description, quantity, unit_price, tax_amount, line_total, quantity_received, quantity_invoiced, warehouse_id, created_at, updated_at) VALUES
    (pol3_1, po3, 1, prod8, 'Steel Sheet', 500, 15000, 900000, 8400000, 500, 500, wh_main, NOW(), NOW());

    -- PO-004: Confirmed, received & invoiced (Local Office - Supplies, no product)
    INSERT INTO purchase_orders (id, tenant_id, organization_id, order_number, vendor_id, order_date, expected_date, subtotal, tax_amount, total_amount, status, payment_status, payment_terms, warehouse_id, currency_id, created_by, created_at, updated_at) VALUES
    (po4, v_tenant_id, v_org_id, 'PO-2026-004', ct_vendor5, '2026-01-20', '2026-01-25', 5000000, 600000, 5600000, 'confirmed', 'paid', 15, wh_main, cur_uzs, v_user_id, NOW(), NOW());
    INSERT INTO purchase_order_lines (id, purchase_order_id, line_number, product_id, description, quantity, unit_price, tax_amount, line_total, quantity_received, quantity_invoiced, warehouse_id, created_at, updated_at) VALUES
    (pol4_1, po4, 1, prod2, 'Office Supplies', 1, 5000000, 600000, 5600000, 1, 1, wh_main, NOW(), NOW());

    -- PO-005: Confirmed, received & invoiced (Turkish - Desks)
    INSERT INTO purchase_orders (id, tenant_id, organization_id, order_number, vendor_id, order_date, expected_date, subtotal, tax_amount, total_amount, status, payment_status, payment_terms, warehouse_id, currency_id, created_by, created_at, updated_at) VALUES
    (po5, v_tenant_id, v_org_id, 'PO-2026-005', ct_vendor4, '2026-02-01', '2026-02-10', 30000000, 3600000, 33600000, 'confirmed', 'unpaid', 60, wh_main, cur_uzs, v_user_id, NOW(), NOW());
    INSERT INTO purchase_order_lines (id, purchase_order_id, line_number, product_id, description, quantity, unit_price, tax_amount, line_total, quantity_received, quantity_invoiced, warehouse_id, created_at, updated_at) VALUES
    (pol5_1, po5, 1, prod2, 'Office Desk', 20, 1500000, 3600000, 33600000, 20, 20, wh_main, NOW(), NOW());

    -- ============================================================================
    -- 12. SALES DELIVERY ORDERS (5 deliveries for physical goods orders)
    -- ============================================================================

    -- SDO-001: SO-001 delivered (Laptops + Monitors)
    INSERT INTO sales_delivery_orders (id, tenant_id, organization_id, delivery_number, sales_order_id, so_number, customer_id, customer_name, delivery_date, warehouse_id, status, created_by, created_at, updated_at) VALUES
    (sdo1, v_tenant_id, v_org_id, 'DEL-2026-001', so1, 'SO-2026-001', ct_customer1, 'Toshkent Savdo LLC', '2026-01-05', wh_main, 'done', v_user_id, NOW(), NOW());
    INSERT INTO sales_delivery_order_lines (id, delivery_order_id, so_line_id, product_id, product_name, quantity_ordered, quantity_to_deliver, quantity_delivered, unit_price, warehouse_id, created_at) VALUES
    (gen_random_uuid(), sdo1, sol1_1, prod1, 'Laptop Computer', 2, 2, 2, 12000000, wh_main, NOW()),
    (gen_random_uuid(), sdo1, sol1_2, prod5, 'LED Monitor 27"', 3, 3, 3, 4000000,  wh_main, NOW());

    -- SDO-002: SO-002 delivered (Chairs)
    INSERT INTO sales_delivery_orders (id, tenant_id, organization_id, delivery_number, sales_order_id, so_number, customer_id, customer_name, delivery_date, warehouse_id, status, created_by, created_at, updated_at) VALUES
    (sdo2, v_tenant_id, v_org_id, 'DEL-2026-002', so2, 'SO-2026-002', ct_customer2, 'Samarkand Electronics', '2026-01-12', wh_main, 'done', v_user_id, NOW(), NOW());
    INSERT INTO sales_delivery_order_lines (id, delivery_order_id, so_line_id, product_id, product_name, quantity_ordered, quantity_to_deliver, quantity_delivered, unit_price, warehouse_id, created_at) VALUES
    (gen_random_uuid(), sdo2, sol2_1, prod3, 'Office Chair', 5, 5, 5, 3500000, wh_main, NOW());

    -- SDO-003: SO-003 delivered (Desks)
    INSERT INTO sales_delivery_orders (id, tenant_id, organization_id, delivery_number, sales_order_id, so_number, customer_id, customer_name, delivery_date, warehouse_id, status, created_by, created_at, updated_at) VALUES
    (sdo3, v_tenant_id, v_org_id, 'DEL-2026-003', so3, 'SO-2026-003', ct_customer3, 'Bukhara Trading House', '2026-01-20', wh_main, 'done', v_user_id, NOW(), NOW());
    INSERT INTO sales_delivery_order_lines (id, delivery_order_id, so_line_id, product_id, product_name, quantity_ordered, quantity_to_deliver, quantity_delivered, unit_price, warehouse_id, created_at) VALUES
    (gen_random_uuid(), sdo3, sol3_1, prod2, 'Office Desk', 6, 6, 6, 2500000, wh_main, NOW());

    -- SDO-004: SO-004 delivered (Printers)
    INSERT INTO sales_delivery_orders (id, tenant_id, organization_id, delivery_number, sales_order_id, so_number, customer_id, customer_name, delivery_date, warehouse_id, status, created_by, created_at, updated_at) VALUES
    (sdo4, v_tenant_id, v_org_id, 'DEL-2026-004', so4, 'SO-2026-004', ct_customer4, 'Fergana Valley Agro', '2026-01-25', wh_main, 'done', v_user_id, NOW(), NOW());
    INSERT INTO sales_delivery_order_lines (id, delivery_order_id, so_line_id, product_id, product_name, quantity_ordered, quantity_to_deliver, quantity_delivered, unit_price, warehouse_id, created_at) VALUES
    (gen_random_uuid(), sdo4, sol4_1, prod4, 'Printer', 2, 2, 2, 4500000, wh_main, NOW());

    -- SDO-005: SO-005 delivered (Laptops)
    INSERT INTO sales_delivery_orders (id, tenant_id, organization_id, delivery_number, sales_order_id, so_number, customer_id, customer_name, delivery_date, warehouse_id, status, created_by, created_at, updated_at) VALUES
    (sdo5, v_tenant_id, v_org_id, 'DEL-2026-005', so5, 'SO-2026-005', ct_customer5, 'Navoi Mining Supply', '2026-02-01', wh_main, 'done', v_user_id, NOW(), NOW());
    INSERT INTO sales_delivery_order_lines (id, delivery_order_id, so_line_id, product_id, product_name, quantity_ordered, quantity_to_deliver, quantity_delivered, unit_price, warehouse_id, created_at) VALUES
    (gen_random_uuid(), sdo5, sol5_1, prod1, 'Laptop Computer', 4, 4, 4, 12000000, wh_main, NOW());

    -- ============================================================================
    -- 13. GOODS RECEIPTS (5 receipts for purchase orders)
    -- ============================================================================

    -- GR-001: PO-001 received (Laptops from China Import)
    INSERT INTO goods_receipts (id, tenant_id, organization_id, gr_number, purchase_order_id, po_number, supplier_id, supplier_name, receipt_date, received_by, received_by_id, warehouse_id, warehouse_name, status, quality_status, total_quantity, accepted_quantity, created_at, updated_at) VALUES
    (gr1, v_tenant_id, v_org_id, 'GR-2026-001', po1, 'PO-2026-001', ct_vendor1, 'China Import Group', '2026-01-03', 'Admin', v_user_id, wh_main, 'Main Warehouse', 'confirmed', 'accepted', 10, 10, NOW(), NOW());
    INSERT INTO goods_receipt_lines (id, goods_receipt_id, po_line_id, product_id, product_name, product_code, ordered_quantity, received_quantity, accepted_quantity, unit, unit_price, quality_status, created_at, updated_at) VALUES
    (gen_random_uuid(), gr1, pol1_1, prod1, 'Laptop Computer', 'PRD-001', 10, 10, 10, 'pcs', 8000000, 'accepted', NOW(), NOW());

    -- GR-002: PO-002 received (Monitors from Korea Tech)
    INSERT INTO goods_receipts (id, tenant_id, organization_id, gr_number, purchase_order_id, po_number, supplier_id, supplier_name, receipt_date, received_by, received_by_id, warehouse_id, warehouse_name, status, quality_status, total_quantity, accepted_quantity, created_at, updated_at) VALUES
    (gr2, v_tenant_id, v_org_id, 'GR-2026-002', po2, 'PO-2026-002', ct_vendor2, 'Korea Tech Supply', '2026-01-10', 'Admin', v_user_id, wh_main, 'Main Warehouse', 'confirmed', 'accepted', 15, 15, NOW(), NOW());
    INSERT INTO goods_receipt_lines (id, goods_receipt_id, po_line_id, product_id, product_name, product_code, ordered_quantity, received_quantity, accepted_quantity, unit, unit_price, quality_status, created_at, updated_at) VALUES
    (gen_random_uuid(), gr2, pol2_1, prod5, 'LED Monitor 27"', 'PRD-005', 15, 15, 15, 'pcs', 2500000, 'accepted', NOW(), NOW());

    -- GR-003: PO-003 received (Steel from Russian Steel)
    INSERT INTO goods_receipts (id, tenant_id, organization_id, gr_number, purchase_order_id, po_number, supplier_id, supplier_name, receipt_date, received_by, received_by_id, warehouse_id, warehouse_name, status, quality_status, total_quantity, accepted_quantity, created_at, updated_at) VALUES
    (gr3, v_tenant_id, v_org_id, 'GR-2026-003', po3, 'PO-2026-003', ct_vendor3, 'Russian Steel Works', '2026-01-18', 'Admin', v_user_id, wh_main, 'Main Warehouse', 'confirmed', 'accepted', 500, 500, NOW(), NOW());
    INSERT INTO goods_receipt_lines (id, goods_receipt_id, po_line_id, product_id, product_name, product_code, ordered_quantity, received_quantity, accepted_quantity, unit, unit_price, quality_status, created_at, updated_at) VALUES
    (gen_random_uuid(), gr3, pol3_1, prod8, 'Steel Sheet', 'RAW-001', 500, 500, 500, 'kg', 15000, 'accepted', NOW(), NOW());

    -- GR-004: PO-004 received (Office supplies)
    INSERT INTO goods_receipts (id, tenant_id, organization_id, gr_number, purchase_order_id, po_number, supplier_id, supplier_name, receipt_date, received_by, received_by_id, warehouse_id, warehouse_name, status, quality_status, total_quantity, accepted_quantity, created_at, updated_at) VALUES
    (gr4, v_tenant_id, v_org_id, 'GR-2026-004', po4, 'PO-2026-004', ct_vendor5, 'Local Office Supplies', '2026-01-22', 'Admin', v_user_id, wh_main, 'Main Warehouse', 'confirmed', 'accepted', 1, 1, NOW(), NOW());
    INSERT INTO goods_receipt_lines (id, goods_receipt_id, po_line_id, product_id, product_name, product_code, ordered_quantity, received_quantity, accepted_quantity, unit, unit_price, quality_status, created_at, updated_at) VALUES
    (gen_random_uuid(), gr4, pol4_1, prod2, 'Office Supplies', 'PRD-002', 1, 1, 1, 'pcs', 5000000, 'accepted', NOW(), NOW());

    -- GR-005: PO-005 received (Desks from Turkish Textile)
    INSERT INTO goods_receipts (id, tenant_id, organization_id, gr_number, purchase_order_id, po_number, supplier_id, supplier_name, receipt_date, received_by, received_by_id, warehouse_id, warehouse_name, status, quality_status, total_quantity, accepted_quantity, created_at, updated_at) VALUES
    (gr5, v_tenant_id, v_org_id, 'GR-2026-005', po5, 'PO-2026-005', ct_vendor4, 'Turkish Textile Co', '2026-02-05', 'Admin', v_user_id, wh_main, 'Main Warehouse', 'confirmed', 'accepted', 20, 20, NOW(), NOW());
    INSERT INTO goods_receipt_lines (id, goods_receipt_id, po_line_id, product_id, product_name, product_code, ordered_quantity, received_quantity, accepted_quantity, unit, unit_price, quality_status, created_at, updated_at) VALUES
    (gen_random_uuid(), gr5, pol5_1, prod2, 'Office Desk', 'PRD-002', 20, 20, 20, 'pcs', 1500000, 'accepted', NOW(), NOW());

    -- ============================================================================
    -- 14. JOURNAL ENTRIES (16 entries for invoices + expenses + equity)
    -- ============================================================================

    -- JE1: Sales Invoice 1 (40,320,000)
    INSERT INTO journal_entries (id, tenant_id, organization_id, journal_id, entry_number, entry_date, description, status, total_debit, total_credit, posted_at, posted_by, created_by, created_at, updated_at) VALUES
    (je1, v_tenant_id, v_org_id, j_sales, 'SAL/2026/001', '2026-01-05', 'Sales Invoice INV-2026-001', 'posted', 40320000, 40320000, '2026-01-05', v_user_id, v_user_id, NOW(), NOW());
    INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES
    (gen_random_uuid(), je1, acc_ar,          'Accounts Receivable',  40320000, 0,        1, NOW()),
    (gen_random_uuid(), je1, acc_sales_rev,   'Sales Revenue',        0,        36000000, 2, NOW()),
    (gen_random_uuid(), je1, acc_vat_payable, 'VAT Payable',          0,        4320000,  3, NOW());

    -- JE2: Sales Invoice 2 (25,200,000)
    INSERT INTO journal_entries (id, tenant_id, organization_id, journal_id, entry_number, entry_date, description, status, total_debit, total_credit, posted_at, posted_by, created_by, created_at, updated_at) VALUES
    (je2, v_tenant_id, v_org_id, j_sales, 'SAL/2026/002', '2026-01-12', 'Sales Invoice INV-2026-002', 'posted', 25200000, 25200000, '2026-01-12', v_user_id, v_user_id, NOW(), NOW());
    INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES
    (gen_random_uuid(), je2, acc_ar,          'Accounts Receivable',  25200000, 0,        1, NOW()),
    (gen_random_uuid(), je2, acc_sales_rev,   'Sales Revenue',        0,        21300000, 2, NOW()),
    (gen_random_uuid(), je2, acc_service_rev, 'Service Revenue',      0,        1200000,  3, NOW()),
    (gen_random_uuid(), je2, acc_vat_payable, 'VAT Payable',          0,        2700000,  4, NOW());

    -- JE3: Sales Invoice 3 (16,800,000)
    INSERT INTO journal_entries (id, tenant_id, organization_id, journal_id, entry_number, entry_date, description, status, total_debit, total_credit, posted_at, posted_by, created_by, created_at, updated_at) VALUES
    (je3, v_tenant_id, v_org_id, j_sales, 'SAL/2026/003', '2026-01-20', 'Sales Invoice INV-2026-003', 'posted', 16800000, 16800000, '2026-01-20', v_user_id, v_user_id, NOW(), NOW());
    INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES
    (gen_random_uuid(), je3, acc_ar,          'Accounts Receivable',  16800000, 0,        1, NOW()),
    (gen_random_uuid(), je3, acc_sales_rev,   'Sales Revenue',        0,        15000000, 2, NOW()),
    (gen_random_uuid(), je3, acc_vat_payable, 'VAT Payable',          0,        1800000,  3, NOW());

    -- JE4: Sales Invoice 4 (10,080,000)
    INSERT INTO journal_entries (id, tenant_id, organization_id, journal_id, entry_number, entry_date, description, status, total_debit, total_credit, posted_at, posted_by, created_by, created_at, updated_at) VALUES
    (je4, v_tenant_id, v_org_id, j_sales, 'SAL/2026/004', '2026-01-25', 'Sales Invoice INV-2026-004', 'posted', 10080000, 10080000, '2026-01-25', v_user_id, v_user_id, NOW(), NOW());
    INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES
    (gen_random_uuid(), je4, acc_ar,          'Accounts Receivable',  10080000, 0,       1, NOW()),
    (gen_random_uuid(), je4, acc_sales_rev,   'Sales Revenue',        0,        9000000, 2, NOW()),
    (gen_random_uuid(), je4, acc_vat_payable, 'VAT Payable',          0,        1080000, 3, NOW());

    -- JE5: Sales Invoice 5 (53,760,000)
    INSERT INTO journal_entries (id, tenant_id, organization_id, journal_id, entry_number, entry_date, description, status, total_debit, total_credit, posted_at, posted_by, created_by, created_at, updated_at) VALUES
    (je5, v_tenant_id, v_org_id, j_sales, 'SAL/2026/005', '2026-02-01', 'Sales Invoice INV-2026-005', 'posted', 53760000, 53760000, '2026-02-01', v_user_id, v_user_id, NOW(), NOW());
    INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES
    (gen_random_uuid(), je5, acc_ar,          'Accounts Receivable',  53760000, 0,        1, NOW()),
    (gen_random_uuid(), je5, acc_sales_rev,   'Sales Revenue',        0,        48000000, 2, NOW()),
    (gen_random_uuid(), je5, acc_vat_payable, 'VAT Payable',          0,        5760000,  3, NOW());

    -- JE6: Sales Invoice 6 (11,200,000 - service)
    INSERT INTO journal_entries (id, tenant_id, organization_id, journal_id, entry_number, entry_date, description, status, total_debit, total_credit, posted_at, posted_by, created_by, created_at, updated_at) VALUES
    (je6, v_tenant_id, v_org_id, j_sales, 'SAL/2026/006', '2026-02-10', 'Sales Invoice INV-2026-006', 'posted', 11200000, 11200000, '2026-02-10', v_user_id, v_user_id, NOW(), NOW());
    INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES
    (gen_random_uuid(), je6, acc_ar,          'Accounts Receivable',  11200000, 0,        1, NOW()),
    (gen_random_uuid(), je6, acc_service_rev, 'Service Revenue',      0,        10000000, 2, NOW()),
    (gen_random_uuid(), je6, acc_vat_payable, 'VAT Payable',          0,        1200000,  3, NOW());

    -- JE7: Purchase Invoice 1 (89,600,000)
    INSERT INTO journal_entries (id, tenant_id, organization_id, journal_id, entry_number, entry_date, description, status, total_debit, total_credit, posted_at, posted_by, created_by, created_at, updated_at) VALUES
    (je7, v_tenant_id, v_org_id, j_purchase, 'PUR/2026/001', '2026-01-03', 'Purchase Invoice BILL-2026-001', 'posted', 89600000, 89600000, '2026-01-03', v_user_id, v_user_id, NOW(), NOW());
    INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES
    (gen_random_uuid(), je7, acc_cogs,        'Cost of Goods Sold',  80000000, 0,        1, NOW()),
    (gen_random_uuid(), je7, acc_vat_payable, 'Input VAT',           9600000,  0,        2, NOW()),
    (gen_random_uuid(), je7, acc_ap,          'Accounts Payable',    0,        89600000, 3, NOW());

    -- JE8: Purchase Invoice 2 (42,000,000)
    INSERT INTO journal_entries (id, tenant_id, organization_id, journal_id, entry_number, entry_date, description, status, total_debit, total_credit, posted_at, posted_by, created_by, created_at, updated_at) VALUES
    (je8, v_tenant_id, v_org_id, j_purchase, 'PUR/2026/002', '2026-01-10', 'Purchase Invoice BILL-2026-002', 'posted', 42000000, 42000000, '2026-01-10', v_user_id, v_user_id, NOW(), NOW());
    INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES
    (gen_random_uuid(), je8, acc_cogs,        'Cost of Goods Sold',  37500000, 0,        1, NOW()),
    (gen_random_uuid(), je8, acc_vat_payable, 'Input VAT',           4500000,  0,        2, NOW()),
    (gen_random_uuid(), je8, acc_ap,          'Accounts Payable',    0,        42000000, 3, NOW());

    -- JE9: Purchase Invoice 3 (8,400,000)
    INSERT INTO journal_entries (id, tenant_id, organization_id, journal_id, entry_number, entry_date, description, status, total_debit, total_credit, posted_at, posted_by, created_by, created_at, updated_at) VALUES
    (je9, v_tenant_id, v_org_id, j_purchase, 'PUR/2026/003', '2026-01-18', 'Purchase Invoice BILL-2026-003', 'posted', 8400000, 8400000, '2026-01-18', v_user_id, v_user_id, NOW(), NOW());
    INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES
    (gen_random_uuid(), je9, acc_cogs,        'Cost of Goods Sold',  7500000, 0,       1, NOW()),
    (gen_random_uuid(), je9, acc_vat_payable, 'Input VAT',           900000,  0,       2, NOW()),
    (gen_random_uuid(), je9, acc_ap,          'Accounts Payable',    0,       8400000, 3, NOW());

    -- JE10: Purchase Invoice 4 (5,600,000 - office supplies)
    INSERT INTO journal_entries (id, tenant_id, organization_id, journal_id, entry_number, entry_date, description, status, total_debit, total_credit, posted_at, posted_by, created_by, created_at, updated_at) VALUES
    (je10, v_tenant_id, v_org_id, j_purchase, 'PUR/2026/004', '2026-01-22', 'Purchase Invoice BILL-2026-004', 'posted', 5600000, 5600000, '2026-01-22', v_user_id, v_user_id, NOW(), NOW());
    INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES
    (gen_random_uuid(), je10, acc_office,      'Office Supplies',    5000000, 0,       1, NOW()),
    (gen_random_uuid(), je10, acc_vat_payable, 'Input VAT',          600000,  0,       2, NOW()),
    (gen_random_uuid(), je10, acc_ap,          'Accounts Payable',   0,       5600000, 3, NOW());

    -- JE11: Purchase Invoice 5 (33,600,000)
    INSERT INTO journal_entries (id, tenant_id, organization_id, journal_id, entry_number, entry_date, description, status, total_debit, total_credit, posted_at, posted_by, created_by, created_at, updated_at) VALUES
    (je11, v_tenant_id, v_org_id, j_purchase, 'PUR/2026/005', '2026-02-05', 'Purchase Invoice BILL-2026-005', 'posted', 33600000, 33600000, '2026-02-05', v_user_id, v_user_id, NOW(), NOW());
    INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES
    (gen_random_uuid(), je11, acc_cogs,        'Cost of Goods Sold',  30000000, 0,        1, NOW()),
    (gen_random_uuid(), je11, acc_vat_payable, 'Input VAT',           3600000,  0,        2, NOW()),
    (gen_random_uuid(), je11, acc_ap,          'Accounts Payable',    0,        33600000, 3, NOW());

    -- JE12: Rent & Utilities (January)
    INSERT INTO journal_entries (id, tenant_id, organization_id, journal_id, entry_number, entry_date, description, status, total_debit, total_credit, posted_at, posted_by, created_by, created_at, updated_at) VALUES
    (je12, v_tenant_id, v_org_id, j_general, 'GEN/2026/001', '2026-01-31', 'January rent and utilities', 'posted', 12000000, 12000000, '2026-01-31', v_user_id, v_user_id, NOW(), NOW());
    INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES
    (gen_random_uuid(), je12, acc_rent,      'January rent',       8000000,  0,        1, NOW()),
    (gen_random_uuid(), je12, acc_utilities, 'January utilities',  4000000,  0,        2, NOW()),
    (gen_random_uuid(), je12, acc_bank,      'Bank payment',       0,        12000000, 3, NOW());

    -- JE13: Salaries (January)
    INSERT INTO journal_entries (id, tenant_id, organization_id, journal_id, entry_number, entry_date, description, status, total_debit, total_credit, posted_at, posted_by, created_by, created_at, updated_at) VALUES
    (je13, v_tenant_id, v_org_id, j_general, 'GEN/2026/002', '2026-01-31', 'January salaries', 'posted', 20000000, 20000000, '2026-01-31', v_user_id, v_user_id, NOW(), NOW());
    INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES
    (gen_random_uuid(), je13, acc_salaries,      'Salaries expense',  20000000, 0,        1, NOW()),
    (gen_random_uuid(), je13, acc_wages_payable, 'Wages payable',     0,        20000000, 2, NOW());

    -- JE14: Charter Capital contribution
    INSERT INTO journal_entries (id, tenant_id, organization_id, journal_id, entry_number, entry_date, description, status, total_debit, total_credit, posted_at, posted_by, created_by, created_at, updated_at) VALUES
    (je14, v_tenant_id, v_org_id, j_general, 'GEN/2026/003', '2026-01-01', 'Initial charter capital', 'posted', 500000000, 500000000, '2026-01-01', v_user_id, v_user_id, NOW(), NOW());
    INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES
    (gen_random_uuid(), je14, acc_bank,      'Capital deposit to bank', 500000000, 0,         1, NOW()),
    (gen_random_uuid(), je14, acc_share_cap, 'Share capital',           0,         500000000, 2, NOW());

    -- JE15: Marketing & Transport (February)
    INSERT INTO journal_entries (id, tenant_id, organization_id, journal_id, entry_number, entry_date, description, status, total_debit, total_credit, posted_at, posted_by, created_by, created_at, updated_at) VALUES
    (je15, v_tenant_id, v_org_id, j_general, 'GEN/2026/004', '2026-02-10', 'February marketing and transport', 'posted', 8000000, 8000000, '2026-02-10', v_user_id, v_user_id, NOW(), NOW());
    INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES
    (gen_random_uuid(), je15, acc_marketing, 'Marketing campaign',  5000000, 0,       1, NOW()),
    (gen_random_uuid(), je15, acc_travel,    'Travel expenses',     3000000, 0,       2, NOW()),
    (gen_random_uuid(), je15, acc_bank,      'Bank payment',        0,       8000000, 3, NOW());

    -- JE16: Interest & Bank Fees (January)
    INSERT INTO journal_entries (id, tenant_id, organization_id, journal_id, entry_number, entry_date, description, status, total_debit, total_credit, posted_at, posted_by, created_by, created_at, updated_at) VALUES
    (je16, v_tenant_id, v_org_id, j_general, 'GEN/2026/005', '2026-01-31', 'Interest and bank fees - January', 'posted', 2500000, 2500000, '2026-01-31', v_user_id, v_user_id, NOW(), NOW());
    INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES
    (gen_random_uuid(), je16, acc_interest_exp, 'Loan interest',  2000000, 0,       1, NOW()),
    (gen_random_uuid(), je16, acc_bank_fees,    'Bank fees',      500000,  0,       2, NOW()),
    (gen_random_uuid(), je16, acc_bank,         'Bank deduction', 0,       2500000, 3, NOW());

    -- ============================================================================
    -- 11. SALES INVOICES (6 invoices)
    -- ============================================================================

    -- SI-001: Fully paid
    INSERT INTO sales_invoices (id, tenant_id, organization_id, invoice_number, sales_order_id, customer_id, customer_name, invoice_date, due_date, subtotal, tax_amount, total_amount, amount_paid, status, currency_id, journal_entry_id, created_at, updated_at) VALUES
    (si1, v_tenant_id, v_org_id, 'INV-2026-001', so1, ct_customer1, 'Toshkent Savdo LLC', '2026-01-05', '2026-02-04', 36000000, 4320000, 40320000, 40320000, 'posted', cur_uzs, je1, NOW(), NOW());
    INSERT INTO sales_invoice_lines (id, sales_invoice_id, sales_order_line_id, line_number, product_id, description, quantity, unit_price, tax_amount, line_total, account_id, created_at) VALUES
    (gen_random_uuid(), si1, sol1_1, 1, prod1, 'Laptop Computer', 2, 12000000, 2880000, 26880000, acc_sales_rev, NOW()),
    (gen_random_uuid(), si1, sol1_2, 2, prod5, 'LED Monitor 27"', 3, 4000000,  1440000, 13440000, acc_sales_rev, NOW());

    -- SI-002: Partially paid
    INSERT INTO sales_invoices (id, tenant_id, organization_id, invoice_number, sales_order_id, customer_id, customer_name, invoice_date, due_date, subtotal, tax_amount, total_amount, amount_paid, status, currency_id, journal_entry_id, created_at, updated_at) VALUES
    (si2, v_tenant_id, v_org_id, 'INV-2026-002', so2, ct_customer2, 'Samarkand Electronics', '2026-01-12', '2026-01-27', 22500000, 2700000, 25200000, 15000000, 'posted', cur_uzs, je2, NOW(), NOW());
    INSERT INTO sales_invoice_lines (id, sales_invoice_id, sales_order_line_id, line_number, product_id, description, quantity, unit_price, tax_amount, line_total, account_id, created_at) VALUES
    (gen_random_uuid(), si2, sol2_1, 1, prod3, 'Office Chair',          5, 3500000, 2100000, 19600000, acc_sales_rev, NOW()),
    (gen_random_uuid(), si2, sol2_2, 2, prod7, 'Software Installation', 4, 300000,  144000,  1344000,  acc_service_rev, NOW());

    -- SI-003: Unpaid
    INSERT INTO sales_invoices (id, tenant_id, organization_id, invoice_number, sales_order_id, customer_id, customer_name, invoice_date, due_date, subtotal, tax_amount, total_amount, amount_paid, status, currency_id, journal_entry_id, created_at, updated_at) VALUES
    (si3, v_tenant_id, v_org_id, 'INV-2026-003', so3, ct_customer3, 'Bukhara Trading House', '2026-01-20', '2026-02-19', 15000000, 1800000, 16800000, 0, 'posted', cur_uzs, je3, NOW(), NOW());
    INSERT INTO sales_invoice_lines (id, sales_invoice_id, sales_order_line_id, line_number, product_id, description, quantity, unit_price, tax_amount, line_total, account_id, created_at) VALUES
    (gen_random_uuid(), si3, sol3_1, 1, prod2, 'Office Desk', 6, 2500000, 1800000, 16800000, acc_sales_rev, NOW());

    -- SI-004: Fully paid
    INSERT INTO sales_invoices (id, tenant_id, organization_id, invoice_number, sales_order_id, customer_id, customer_name, invoice_date, due_date, subtotal, tax_amount, total_amount, amount_paid, status, currency_id, journal_entry_id, created_at, updated_at) VALUES
    (si4, v_tenant_id, v_org_id, 'INV-2026-004', so4, ct_customer4, 'Fergana Valley Agro', '2026-01-25', '2026-03-11', 9000000, 1080000, 10080000, 10080000, 'posted', cur_uzs, je4, NOW(), NOW());
    INSERT INTO sales_invoice_lines (id, sales_invoice_id, sales_order_line_id, line_number, product_id, description, quantity, unit_price, tax_amount, line_total, account_id, created_at) VALUES
    (gen_random_uuid(), si4, sol4_1, 1, prod4, 'Printer', 2, 4500000, 1080000, 10080000, acc_sales_rev, NOW());

    -- SI-005: Unpaid
    INSERT INTO sales_invoices (id, tenant_id, organization_id, invoice_number, sales_order_id, customer_id, customer_name, invoice_date, due_date, subtotal, tax_amount, total_amount, amount_paid, status, currency_id, journal_entry_id, created_at, updated_at) VALUES
    (si5, v_tenant_id, v_org_id, 'INV-2026-005', so5, ct_customer5, 'Navoi Mining Supply', '2026-02-01', '2026-03-03', 48000000, 5760000, 53760000, 0, 'posted', cur_uzs, je5, NOW(), NOW());
    INSERT INTO sales_invoice_lines (id, sales_invoice_id, sales_order_line_id, line_number, product_id, description, quantity, unit_price, tax_amount, line_total, account_id, created_at) VALUES
    (gen_random_uuid(), si5, sol5_1, 1, prod1, 'Laptop Computer', 4, 12000000, 5760000, 53760000, acc_sales_rev, NOW());

    -- SI-006: Service invoice, partially paid
    INSERT INTO sales_invoices (id, tenant_id, organization_id, invoice_number, sales_order_id, customer_id, customer_name, invoice_date, due_date, subtotal, tax_amount, total_amount, amount_paid, status, currency_id, journal_entry_id, created_at, updated_at) VALUES
    (si6, v_tenant_id, v_org_id, 'INV-2026-006', so6, ct_customer1, 'Toshkent Savdo LLC', '2026-02-10', '2026-03-12', 10000000, 1200000, 11200000, 5000000, 'posted', cur_uzs, je6, NOW(), NOW());
    INSERT INTO sales_invoice_lines (id, sales_invoice_id, sales_order_line_id, line_number, product_id, description, quantity, unit_price, tax_amount, line_total, account_id, created_at) VALUES
    (gen_random_uuid(), si6, sol6_1, 1, prod6, 'IT Consulting', 20, 500000, 1200000, 11200000, acc_service_rev, NOW());

    -- ============================================================================
    -- 12. PURCHASE INVOICES (5 invoices)
    -- ============================================================================

    -- PI-001: Fully paid
    INSERT INTO purchase_invoices (id, tenant_id, organization_id, invoice_number, purchase_order_id, vendor_id, supplier_name, invoice_date, due_date, subtotal, tax_amount, total_amount, paid_amount, status, payment_status, currency_id, journal_entry_id, created_at, updated_at) VALUES
    (pi1, v_tenant_id, v_org_id, 'BILL-2026-001', po1, ct_vendor1, 'China Import Group', '2026-01-03', '2026-02-02', 80000000, 9600000, 89600000, 89600000, 'posted', 'paid', cur_uzs, je7, NOW(), NOW());
    INSERT INTO purchase_invoice_lines (id, purchase_invoice_id, purchase_order_line_id, line_number, product_id, description, quantity, unit_price, tax_amount, line_total, account_id, created_at) VALUES
    (gen_random_uuid(), pi1, pol1_1, 1, prod1, 'Laptop Computer', 10, 8000000, 9600000, 89600000, acc_cogs, NOW());

    -- PI-002: Partially paid
    INSERT INTO purchase_invoices (id, tenant_id, organization_id, invoice_number, purchase_order_id, vendor_id, supplier_name, invoice_date, due_date, subtotal, tax_amount, total_amount, paid_amount, status, payment_status, currency_id, journal_entry_id, created_at, updated_at) VALUES
    (pi2, v_tenant_id, v_org_id, 'BILL-2026-002', po2, ct_vendor2, 'Korea Tech Supply', '2026-01-10', '2026-02-24', 37500000, 4500000, 42000000, 20000000, 'posted', 'partial', cur_uzs, je8, NOW(), NOW());
    INSERT INTO purchase_invoice_lines (id, purchase_invoice_id, purchase_order_line_id, line_number, product_id, description, quantity, unit_price, tax_amount, line_total, account_id, created_at) VALUES
    (gen_random_uuid(), pi2, pol2_1, 1, prod5, 'LED Monitor 27"', 15, 2500000, 4500000, 42000000, acc_cogs, NOW());

    -- PI-003: Unpaid
    INSERT INTO purchase_invoices (id, tenant_id, organization_id, invoice_number, purchase_order_id, vendor_id, supplier_name, invoice_date, due_date, subtotal, tax_amount, total_amount, paid_amount, status, payment_status, currency_id, journal_entry_id, created_at, updated_at) VALUES
    (pi3, v_tenant_id, v_org_id, 'BILL-2026-003', po3, ct_vendor3, 'Russian Steel Works', '2026-01-18', '2026-02-17', 7500000, 900000, 8400000, 0, 'posted', 'unpaid', cur_uzs, je9, NOW(), NOW());
    INSERT INTO purchase_invoice_lines (id, purchase_invoice_id, purchase_order_line_id, line_number, product_id, description, quantity, unit_price, tax_amount, line_total, account_id, created_at) VALUES
    (gen_random_uuid(), pi3, pol3_1, 1, prod8, 'Steel Sheet', 500, 15000, 900000, 8400000, acc_cogs, NOW());

    -- PI-004: Fully paid (office supplies)
    INSERT INTO purchase_invoices (id, tenant_id, organization_id, invoice_number, purchase_order_id, vendor_id, supplier_name, invoice_date, due_date, subtotal, tax_amount, total_amount, paid_amount, status, payment_status, currency_id, journal_entry_id, created_at, updated_at) VALUES
    (pi4, v_tenant_id, v_org_id, 'BILL-2026-004', po4, ct_vendor5, 'Local Office Supplies', '2026-01-22', '2026-02-06', 5000000, 600000, 5600000, 5600000, 'posted', 'paid', cur_uzs, je10, NOW(), NOW());
    INSERT INTO purchase_invoice_lines (id, purchase_invoice_id, purchase_order_line_id, line_number, product_id, description, quantity, unit_price, tax_amount, line_total, account_id, created_at) VALUES
    (gen_random_uuid(), pi4, pol4_1, 1, NULL, 'Office supplies assortment', 1, 5000000, 600000, 5600000, acc_office, NOW());

    -- PI-005: Unpaid (furniture)
    INSERT INTO purchase_invoices (id, tenant_id, organization_id, invoice_number, purchase_order_id, vendor_id, supplier_name, invoice_date, due_date, subtotal, tax_amount, total_amount, paid_amount, status, payment_status, currency_id, journal_entry_id, created_at, updated_at) VALUES
    (pi5, v_tenant_id, v_org_id, 'BILL-2026-005', po5, ct_vendor4, 'Turkish Textile Co', '2026-02-05', '2026-04-06', 30000000, 3600000, 33600000, 0, 'posted', 'unpaid', cur_uzs, je11, NOW(), NOW());
    INSERT INTO purchase_invoice_lines (id, purchase_invoice_id, purchase_order_line_id, line_number, product_id, description, quantity, unit_price, tax_amount, line_total, account_id, created_at) VALUES
    (gen_random_uuid(), pi5, pol5_1, 1, prod2, 'Office Desk', 20, 1500000, 3600000, 33600000, acc_cogs, NOW());

    -- ============================================================================
    -- 13. PAYMENTS (4 customer receipts + 4 vendor payments)
    -- ============================================================================

    -- Customer Receipts (inbound)
    INSERT INTO payments (id, tenant_id, organization_id, payment_number, type, contact_id, payment_method_id, payment_date, amount, currency_id, reference, notes, status, bank_account_id, created_by, created_at, updated_at) VALUES
    (pay1, v_tenant_id, v_org_id, 'REC/2026/001', 'receipt', ct_customer1, pm_bank, '2026-01-15', 40320000, cur_uzs, 'INV-2026-001', 'Toshkent Savdo full payment',        'confirmed', acc_bank, v_user_id, NOW(), NOW()),
    (pay2, v_tenant_id, v_org_id, 'REC/2026/002', 'receipt', ct_customer2, pm_cash, '2026-01-20', 15000000, cur_uzs, 'INV-2026-002', 'Samarkand Electronics partial',       'confirmed', NULL,    v_user_id, NOW(), NOW()),
    (pay3, v_tenant_id, v_org_id, 'REC/2026/003', 'receipt', ct_customer4, pm_bank, '2026-02-05', 10080000, cur_uzs, 'INV-2026-004', 'Fergana Valley Agro full payment',    'confirmed', acc_bank, v_user_id, NOW(), NOW()),
    (pay4, v_tenant_id, v_org_id, 'REC/2026/004', 'receipt', ct_customer1, pm_bank, '2026-02-12', 5000000,  cur_uzs, 'INV-2026-006', 'Toshkent Savdo consulting partial',   'confirmed', acc_bank, v_user_id, NOW(), NOW());

    -- Payment Allocations for customer receipts
    INSERT INTO payment_allocations (id, payment_id, document_type, document_id, amount, created_at) VALUES
    (gen_random_uuid(), pay1, 'sales_invoice', si1, 40320000, NOW()),
    (gen_random_uuid(), pay2, 'sales_invoice', si2, 15000000, NOW()),
    (gen_random_uuid(), pay3, 'sales_invoice', si4, 10080000, NOW()),
    (gen_random_uuid(), pay4, 'sales_invoice', si6, 5000000,  NOW());

    -- Vendor Payments (outbound)
    INSERT INTO payments (id, tenant_id, organization_id, payment_number, type, contact_id, payment_method_id, payment_date, amount, currency_id, reference, notes, status, bank_account_id, created_by, created_at, updated_at) VALUES
    (pay5, v_tenant_id, v_org_id, 'PAY/2026/001', 'payment', ct_vendor1, pm_bank, '2026-01-20', 89600000, cur_uzs, 'BILL-2026-001', 'China Import Group full payment',    'confirmed', acc_bank, v_user_id, NOW(), NOW()),
    (pay6, v_tenant_id, v_org_id, 'PAY/2026/002', 'payment', ct_vendor2, pm_bank, '2026-01-25', 20000000, cur_uzs, 'BILL-2026-002', 'Korea Tech Supply partial',           'confirmed', acc_bank, v_user_id, NOW(), NOW()),
    (pay7, v_tenant_id, v_org_id, 'PAY/2026/003', 'payment', ct_vendor5, pm_cash, '2026-02-01', 5600000,  cur_uzs, 'BILL-2026-004', 'Local Office Supplies full payment', 'confirmed', NULL,    v_user_id, NOW(), NOW()),
    (pay8, v_tenant_id, v_org_id, 'PAY/2026/004', 'payment', ct_vendor5, pm_bank, '2026-02-05', 20000000, cur_uzs, 'January salaries', 'Employee salaries for January',    'confirmed', acc_bank, v_user_id, NOW(), NOW());

    -- Payment Allocations for vendor payments
    INSERT INTO payment_allocations (id, payment_id, document_type, document_id, amount, created_at) VALUES
    (gen_random_uuid(), pay5, 'purchase_invoice', pi1, 89600000, NOW()),
    (gen_random_uuid(), pay6, 'purchase_invoice', pi2, 20000000, NOW()),
    (gen_random_uuid(), pay7, 'purchase_invoice', pi4, 5600000,  NOW());

    -- ============================================================================
    -- 14. FIXED ASSETS
    -- ============================================================================
    INSERT INTO fixed_assets (id, tenant_id, organization_id, asset_code, name, description, category_name, acquisition_date, acquisition_cost, salvage_value, useful_life_months, depreciation_method, accumulated_depreciation, book_value, location, status, created_by, created_at, updated_at) VALUES
    (fa1, v_tenant_id, v_org_id, 'FA-001', 'Office Building',  'Main office building',            'Buildings',  '2025-01-01', 300000000, 30000000, 240, 'straight_line', 15000000, 285000000, 'Tashkent', 'active', v_user_id, NOW(), NOW()),
    (fa2, v_tenant_id, v_org_id, 'FA-002', 'Delivery Truck',   'Isuzu NPR delivery truck',        'Vehicles',   '2025-06-01', 120000000, 12000000, 120, 'straight_line', 9000000,  111000000, 'Tashkent', 'active', v_user_id, NOW(), NOW()),
    (fa3, v_tenant_id, v_org_id, 'FA-003', 'Server Equipment', 'Dell PowerEdge R750 server rack', 'Equipment',  '2025-09-01', 80000000,  8000000,  60,  'straight_line', 8000000,  72000000,  'Tashkent', 'active', v_user_id, NOW(), NOW());

    -- ============================================================================
    -- 22. INVENTORY TRANSACTIONS (stock movements)
    -- ============================================================================

    -- Receipts from purchase orders (goods in)
    INSERT INTO inventory_transactions (id, tenant_id, organization_id, inventory_id, transaction_type, reference_type, reference_id, quantity, unit_cost, total_cost, to_warehouse_id, to_location_id, reason, notes, transaction_date, created_by, created_at) VALUES
    (gen_random_uuid(), v_tenant_id, v_org_id, (SELECT id FROM inventory WHERE tenant_id = v_tenant_id AND product_id = prod1 LIMIT 1), 'receipt', 'goods_receipt', gr1, 10, 8000000, 80000000, wh_main, wl_stock, 'Purchase receipt', 'GR-2026-001: Laptops from China Import', '2026-01-03', v_user_id, NOW()),
    (gen_random_uuid(), v_tenant_id, v_org_id, (SELECT id FROM inventory WHERE tenant_id = v_tenant_id AND product_id = prod5 LIMIT 1), 'receipt', 'goods_receipt', gr2, 15, 2500000, 37500000, wh_main, wl_stock, 'Purchase receipt', 'GR-2026-002: Monitors from Korea Tech', '2026-01-10', v_user_id, NOW()),
    (gen_random_uuid(), v_tenant_id, v_org_id, (SELECT id FROM inventory WHERE tenant_id = v_tenant_id AND product_id = prod8 LIMIT 1), 'receipt', 'goods_receipt', gr3, 500, 15000, 7500000, wh_main, wl_stock, 'Purchase receipt', 'GR-2026-003: Steel from Russian Steel', '2026-01-18', v_user_id, NOW()),
    (gen_random_uuid(), v_tenant_id, v_org_id, (SELECT id FROM inventory WHERE tenant_id = v_tenant_id AND product_id = prod2 LIMIT 1), 'receipt', 'goods_receipt', gr5, 20, 1500000, 30000000, wh_main, wl_stock, 'Purchase receipt', 'GR-2026-005: Desks from Turkish Textile', '2026-02-05', v_user_id, NOW());

    -- Shipments from sales orders (goods out)
    INSERT INTO inventory_transactions (id, tenant_id, organization_id, inventory_id, transaction_type, reference_type, reference_id, quantity, unit_cost, total_cost, from_warehouse_id, from_location_id, reason, notes, transaction_date, created_by, created_at) VALUES
    (gen_random_uuid(), v_tenant_id, v_org_id, (SELECT id FROM inventory WHERE tenant_id = v_tenant_id AND product_id = prod1 LIMIT 1), 'shipment', 'sales_delivery', sdo1, -2, 8000000, -16000000, wh_main, wl_stock, 'Sales delivery', 'DEL-2026-001: Laptops to Toshkent Savdo', '2026-01-05', v_user_id, NOW()),
    (gen_random_uuid(), v_tenant_id, v_org_id, (SELECT id FROM inventory WHERE tenant_id = v_tenant_id AND product_id = prod5 LIMIT 1), 'shipment', 'sales_delivery', sdo1, -3, 2500000, -7500000, wh_main, wl_stock, 'Sales delivery', 'DEL-2026-001: Monitors to Toshkent Savdo', '2026-01-05', v_user_id, NOW()),
    (gen_random_uuid(), v_tenant_id, v_org_id, (SELECT id FROM inventory WHERE tenant_id = v_tenant_id AND product_id = prod3 LIMIT 1), 'shipment', 'sales_delivery', sdo2, -5, 2000000, -10000000, wh_main, wl_stock, 'Sales delivery', 'DEL-2026-002: Chairs to Samarkand', '2026-01-12', v_user_id, NOW()),
    (gen_random_uuid(), v_tenant_id, v_org_id, (SELECT id FROM inventory WHERE tenant_id = v_tenant_id AND product_id = prod2 LIMIT 1), 'shipment', 'sales_delivery', sdo3, -6, 1500000, -9000000, wh_main, wl_stock, 'Sales delivery', 'DEL-2026-003: Desks to Bukhara', '2026-01-20', v_user_id, NOW()),
    (gen_random_uuid(), v_tenant_id, v_org_id, (SELECT id FROM inventory WHERE tenant_id = v_tenant_id AND product_id = prod4 LIMIT 1), 'shipment', 'sales_delivery', sdo4, -2, 3000000, -6000000, wh_main, wl_stock, 'Sales delivery', 'DEL-2026-004: Printers to Fergana', '2026-01-25', v_user_id, NOW()),
    (gen_random_uuid(), v_tenant_id, v_org_id, (SELECT id FROM inventory WHERE tenant_id = v_tenant_id AND product_id = prod1 LIMIT 1), 'shipment', 'sales_delivery', sdo5, -4, 8000000, -32000000, wh_main, wl_stock, 'Sales delivery', 'DEL-2026-005: Laptops to Navoi Mining', '2026-02-01', v_user_id, NOW());

    -- ============================================================================
    -- 23. RECALCULATE ALL ACCOUNT BALANCES
    -- ============================================================================
    WITH balance_calc AS (
        SELECT jel.account_id,
               SUM(jel.debit_amount) - SUM(jel.credit_amount) as net_balance
        FROM journal_entry_lines jel
        JOIN journal_entries je ON jel.journal_entry_id = je.id
        WHERE je.tenant_id = v_tenant_id
          AND je.status = 'posted'
          AND je.deleted_at IS NULL
        GROUP BY jel.account_id
    )
    UPDATE accounts a
    SET current_balance = COALESCE(bc.net_balance, 0),
        updated_at = NOW()
    FROM balance_calc bc
    WHERE a.id = bc.account_id
      AND a.tenant_id = v_tenant_id;

    RAISE NOTICE '';
    RAISE NOTICE '================================================';
    RAISE NOTICE '  Demo seed data inserted successfully!';
    RAISE NOTICE '================================================';
    RAISE NOTICE '  Tenant:  Demo Company';
    RAISE NOTICE '  Login:   demo@genixerp.com';
    RAISE NOTICE '------------------------------------------------';
    RAISE NOTICE '  10 contacts (5 customers + 5 vendors)';
    RAISE NOTICE '  8 products (5 goods + 2 services + 1 raw)';
    RAISE NOTICE '  3 product categories';
    RAISE NOTICE '  5 units of measure';
    RAISE NOTICE '  3 payment methods + 1 bank account';
    RAISE NOTICE '  6 sales orders (all confirmed)';
    RAISE NOTICE '  5 purchase orders (all confirmed)';
    RAISE NOTICE '  5 sales delivery orders (all done)';
    RAISE NOTICE '  5 goods receipts (all confirmed)';
    RAISE NOTICE '  6 sales invoices (2 paid, 2 partial, 2 unpaid)';
    RAISE NOTICE '  5 purchase invoices (2 paid, 1 partial, 2 unpaid)';
    RAISE NOTICE '  16 journal entries (all posted)';
    RAISE NOTICE '  8 payments (4 receipts + 4 payments)';
    RAISE NOTICE '  3 fixed assets';
    RAISE NOTICE '  6 inventory records + 10 transactions';
    RAISE NOTICE '  Account balances recalculated';
    RAISE NOTICE '================================================';

END;
$$;

-- ============================================================================
-- ISH JARAYONLARI: demo automation rules
-- ============================================================================
DO $$
DECLARE
    v_tid UUID := 'df372dc3-0b77-4ec6-aef3-c1145ecbeaac';
    v_user_id UUID := '43aa617b-1df4-463e-84cf-baf71a982e20';
BEGIN
    INSERT INTO workflow_rules (id, tenant_id, name, description, category, trigger_type,
        trigger_event, conditions, actions, is_active, priority, created_by, created_at, updated_at)
    VALUES
    (uuid_generate_v4(), v_tid,
     'Zaxira kamaysa — bildirishnoma',
     'Mahsulot zaxirasi qayta buyurtma nuqtasidan pastga tushganda barcha foydalanuvchilarga bildirishnoma yuboradi',
     'inventory', 'event', 'inventory.low_stock',
     '{}',
     '[{"type":"create_notification","config":{"recipient_type":"all","title":"Zaxira kam: {{product_name}}","message":"{{product_name}} ({{product_code}}) zaxirasi {{available}} donagacha kamaydi (chegara: {{reorder_point}})."}}]',
     true, 0, v_user_id, NOW(), NOW()),
    (uuid_generate_v4(), v_tid,
     'Yangi lid — sotuv jamoasiga xabar',
     'CRM''da yangi lid yaratilganda administratorlarga bildirishnoma yuboradi',
     'crm', 'event', 'lead.created',
     '{}',
     '[{"type":"create_notification","config":{"recipient_type":"roles","roles":["owner","admin"],"title":"Yangi lid: {{contact_name}}","message":"{{company_name}} kompaniyasidan yangi lid keldi ({{source}}). Kutilayotgan qiymat: {{expected_value}} so''m."}}]',
     true, 0, v_user_id, NOW(), NOW()),
    (uuid_generate_v4(), v_tid,
     'Hisob-faktura muddati o''tdi — eslatma',
     'To''lanmagan hisob-faktura muddati o''tganda (har bir faktura uchun bir marta) egasiga bildirishnoma yuboradi',
     'sales', 'scheduled', 'invoice.overdue',
     '{"logic":"and","conditions":[{"field":"total_amount","operator":"gt","value":0}]}',
     '[{"type":"create_notification","config":{"recipient_type":"roles","roles":["owner","admin"],"title":"Muddati o''tgan faktura: {{invoice_number}}","message":"{{customer_name}} uchun {{invoice_number}} fakturaning muddati {{days_overdue}} kun oldin o''tgan. Summa: {{total_amount}} so''m."}}]',
     true, 0, v_user_id, NOW(), NOW());

    RAISE NOTICE '  3 demo automation rules created';
END;
$$;

-- ============================================================================
-- SHARTNOMALAR: demo contracts (statuses across the lifecycle; one expiring
-- soon, one with an amendment) — see docs/shartnomalar-changelog.md
-- ============================================================================
DO $$
DECLARE
    v_tid UUID := 'df372dc3-0b77-4ec6-aef3-c1145ecbeaac';
    v_org_id UUID := '043e7051-6765-4431-92ac-015773fe9e0b';
    v_user_id UUID := '43aa617b-1df4-463e-84cf-baf71a982e20';
    v_customer1 UUID;
    v_customer2 UUID;
    v_vendor1 UUID;
    v_vendor2 UUID;
    v_resp UUID;
    v_c1 UUID := uuid_generate_v4();
    v_c2 UUID := uuid_generate_v4();
    v_c3 UUID := uuid_generate_v4();
    v_c4 UUID := uuid_generate_v4();
BEGIN
    -- Reset previous demo contracts (children cascade).
    DELETE FROM procurement_contracts WHERE tenant_id = v_tid;

    SELECT id INTO v_customer1 FROM contacts WHERE tenant_id = v_tid AND code = 'C-001';
    SELECT id INTO v_customer2 FROM contacts WHERE tenant_id = v_tid AND code = 'C-002';
    SELECT id INTO v_vendor1   FROM contacts WHERE tenant_id = v_tid AND code = 'V-001';
    SELECT id INTO v_vendor2   FROM contacts WHERE tenant_id = v_tid AND code = 'V-002';
    SELECT id INTO v_resp      FROM employees WHERE tenant_id = v_tid AND deleted_at IS NULL ORDER BY created_at LIMIT 1;

    IF v_customer1 IS NULL OR v_vendor1 IS NULL THEN
        RAISE NOTICE '  Shartnomalar seed skipped: demo contacts missing';
        RETURN;
    END IF;

    INSERT INTO procurement_contracts (
        id, tenant_id, organization_id, contract_number, title, vendor_id, vendor_name,
        direction, contract_type, status, start_date, end_date, signed_date,
        value, currency, description, responsible_employee_id, created_by, created_at, updated_at
    ) VALUES
    -- 1. Amaldagi bosh pudrat (income) with an amendment below
    (v_c1, v_tid, v_org_id, 'CNT-2026-0001', 'Bosh pudrat shartnomasi — Yunusobod TJM',
     v_customer1, 'Toshkent Savdo LLC', 'income', 'project', 'active',
     CURRENT_DATE - 60, CURRENT_DATE + 200, CURRENT_DATE - 58,
     250000000, 'UZS', 'Yunusobod savdo markazi qurilishi bo''yicha bosh pudrat',
     v_resp, v_user_id, NOW() - INTERVAL '60 days', NOW()),
    -- 2. Ta'minot shartnomasi (expense) — expiring within 30 days
    (v_c2, v_tid, v_org_id, 'CNT-2026-0002', 'Qurilish materiallari ta''minoti',
     v_vendor1, 'China Import Group', 'expense', 'annual', 'active',
     CURRENT_DATE - 340, CURRENT_DATE + 20, CURRENT_DATE - 338,
     80000000, 'UZS', 'Sement va armatura yetkazib berish bo''yicha yillik shartnoma',
     v_resp, v_user_id, NOW() - INTERVAL '340 days', NOW()),
    -- 3. Qoralama (income, muddatsiz)
    (v_c3, v_tid, v_org_id, 'CNT-2026-0003', 'Servis xizmatlari shartnomasi',
     v_customer2, 'Samarkand Electronics', 'income', 'monthly', 'draft',
     CURRENT_DATE + 7, NULL, NULL,
     45000000, 'UZS', 'Oylik texnik xizmat ko''rsatish', v_resp, v_user_id, NOW() - INTERVAL '3 days', NOW()),
    -- 4. Yakunlangan (expense)
    (v_c4, v_tid, v_org_id, 'CNT-2025-0090', 'Uskunalar xaridi shartnomasi',
     v_vendor2, 'Korea Tech Supply', 'expense', 'fixed', 'completed',
     CURRENT_DATE - 400, CURRENT_DATE - 40, CURRENT_DATE - 398,
     30000000, 'UZS', 'Ishlab chiqarish uskunalari xaridi', v_resp, v_user_id, NOW() - INTERVAL '400 days', NOW());

    -- Amendment on the bosh pudrat: scope extension +25M
    INSERT INTO contract_amendments (id, tenant_id, contract_id, number, date, amount_delta, description, created_by)
    VALUES (uuid_generate_v4(), v_tid, v_c1, 'Ilova 1', CURRENT_DATE - 20, 25000000,
            'Qo''shimcha qavat pardozlash ishlari', v_user_id);

    RAISE NOTICE '  4 demo contracts + 1 amendment created';
END;
$$;

COMMIT;
