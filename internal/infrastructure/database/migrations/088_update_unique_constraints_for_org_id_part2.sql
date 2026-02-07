-- Migration 088: Update remaining unique constraints to include organization_id
-- Tables that already had organization_id but still had tenant-only unique constraints

-- warehouses: (tenant_id, code) -> (tenant_id, organization_id, code)
ALTER TABLE warehouses DROP CONSTRAINT IF EXISTS warehouses_tenant_id_code_key;
ALTER TABLE warehouses ADD CONSTRAINT warehouses_tenant_org_code_key UNIQUE (tenant_id, organization_id, code);

-- contacts: (tenant_id, code) -> (tenant_id, organization_id, code)
ALTER TABLE contacts DROP CONSTRAINT IF EXISTS contacts_tenant_id_code_key;
ALTER TABLE contacts ADD CONSTRAINT contacts_tenant_org_code_key UNIQUE (tenant_id, organization_id, code);

-- sales_orders: (tenant_id, order_number) -> (tenant_id, organization_id, order_number)
ALTER TABLE sales_orders DROP CONSTRAINT IF EXISTS sales_orders_tenant_id_order_number_key;
ALTER TABLE sales_orders ADD CONSTRAINT sales_orders_tenant_org_order_number_key UNIQUE (tenant_id, organization_id, order_number);

-- sales_invoices: (tenant_id, invoice_number) -> (tenant_id, organization_id, invoice_number)
ALTER TABLE sales_invoices DROP CONSTRAINT IF EXISTS sales_invoices_tenant_id_invoice_number_key;
ALTER TABLE sales_invoices ADD CONSTRAINT sales_invoices_tenant_org_invoice_number_key UNIQUE (tenant_id, organization_id, invoice_number);

-- purchase_orders: (tenant_id, order_number) -> (tenant_id, organization_id, order_number)
ALTER TABLE purchase_orders DROP CONSTRAINT IF EXISTS purchase_orders_tenant_id_order_number_key;
ALTER TABLE purchase_orders ADD CONSTRAINT purchase_orders_tenant_org_order_number_key UNIQUE (tenant_id, organization_id, order_number);

-- purchase_invoices: (tenant_id, invoice_number) -> (tenant_id, organization_id, invoice_number)
ALTER TABLE purchase_invoices DROP CONSTRAINT IF EXISTS purchase_invoices_tenant_id_invoice_number_key;
ALTER TABLE purchase_invoices ADD CONSTRAINT purchase_invoices_tenant_org_invoice_number_key UNIQUE (tenant_id, organization_id, invoice_number);

-- journal_entries: (tenant_id, entry_number) -> (tenant_id, organization_id, entry_number)
ALTER TABLE journal_entries DROP CONSTRAINT IF EXISTS journal_entries_tenant_id_entry_number_key;
ALTER TABLE journal_entries ADD CONSTRAINT journal_entries_tenant_org_entry_number_key UNIQUE (tenant_id, organization_id, entry_number);

-- payments: (tenant_id, payment_number) -> (tenant_id, organization_id, payment_number)
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_tenant_id_payment_number_key;
ALTER TABLE payments ADD CONSTRAINT payments_tenant_org_payment_number_key UNIQUE (tenant_id, organization_id, payment_number);

-- cash_transactions: (tenant_id, transaction_number) -> (tenant_id, organization_id, transaction_number)
ALTER TABLE cash_transactions DROP CONSTRAINT IF EXISTS cash_transactions_tenant_id_transaction_number_key;
ALTER TABLE cash_transactions ADD CONSTRAINT cash_transactions_tenant_org_transaction_number_key UNIQUE (tenant_id, organization_id, transaction_number);

-- bank_accounts: keep as-is — (tenant_id, account_id) references a specific account which is already org-scoped
