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
    v_tid UUID := '554423dd-5013-40a5-b3cf-1c800c2b139c';
BEGIN
    -- ========================================================================
    -- COMPREHENSIVE CLEANUP in reverse FK dependency order
    -- Uses IF EXISTS guards for tables that may not exist on all deployments
    -- ========================================================================

    -- === Manufacturing / Production (references products, contacts, warehouses) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'manufacturing_transfer_lines') THEN
        EXECUTE 'DELETE FROM manufacturing_transfer_lines WHERE transfer_id IN (SELECT id FROM manufacturing_transfers WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'manufacturing_transfers') THEN
        EXECUTE 'DELETE FROM manufacturing_transfers WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'manufacturing_work_orders') THEN
        EXECUTE 'DELETE FROM manufacturing_work_orders WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'production_material_consumption') THEN
        EXECUTE 'DELETE FROM production_material_consumption WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'quality_checks') THEN
        EXECUTE 'DELETE FROM quality_checks WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'quality_control_points') THEN
        EXECUTE 'DELETE FROM quality_control_points WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'mrp_recommendations') THEN
        EXECUTE 'DELETE FROM mrp_recommendations WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'mrp_supply') THEN
        EXECUTE 'DELETE FROM mrp_supply WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'mrp_demand') THEN
        EXECUTE 'DELETE FROM mrp_demand WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'production_orders') THEN
        EXECUTE 'DELETE FROM production_orders WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'manufacturing_equipment') THEN
        EXECUTE 'DELETE FROM manufacturing_equipment WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'work_centers') THEN
        EXECUTE 'DELETE FROM work_centers WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === Intercompany (references warehouses, bank_accounts) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'intercompany_transfer_lines') THEN
        EXECUTE 'DELETE FROM intercompany_transfer_lines WHERE transfer_id IN (SELECT id FROM intercompany_transfers WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'intercompany_payments') THEN
        EXECUTE 'DELETE FROM intercompany_payments WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'intercompany_transfers') THEN
        EXECUTE 'DELETE FROM intercompany_transfers WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'intercompany_rules') THEN
        EXECUTE 'DELETE FROM intercompany_rules WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === Construction (references products, warehouses) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'smeta_resources') THEN
        EXECUTE 'DELETE FROM smeta_resources WHERE smeta_id IN (SELECT id FROM smetas WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'construction_site_warehouses') THEN
        EXECUTE 'DELETE FROM construction_site_warehouses WHERE site_id IN (SELECT id FROM construction_sites WHERE tenant_id = $1)' USING v_tid;
    END IF;

    -- === POS (references products, contacts, warehouses, payment_methods) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'pos_payments') THEN
        EXECUTE 'DELETE FROM pos_payments WHERE order_id IN (SELECT id FROM pos_orders WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'pos_order_lines') THEN
        EXECUTE 'DELETE FROM pos_order_lines WHERE order_id IN (SELECT id FROM pos_orders WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'pos_orders') THEN
        EXECUTE 'DELETE FROM pos_orders WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'pos_configs') THEN
        EXECUTE 'DELETE FROM pos_configs WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'pos_product_categories') THEN
        EXECUTE 'DELETE FROM pos_product_categories WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === Dropshipping (references products, contacts) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'dropship_order_lines') THEN
        EXECUTE 'DELETE FROM dropship_order_lines WHERE order_id IN (SELECT id FROM dropship_orders WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'dropship_orders') THEN
        EXECUTE 'DELETE FROM dropship_orders WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'dropship_product_vendors') THEN
        EXECUTE 'DELETE FROM dropship_product_vendors WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'dropship_vendor_settings') THEN
        EXECUTE 'DELETE FROM dropship_vendor_settings WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === Pricelists (references products, contacts, product_categories) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'customer_pricelists') THEN
        EXECUTE 'DELETE FROM customer_pricelists WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'pricelist_items') THEN
        EXECUTE 'DELETE FROM pricelist_items WHERE pricelist_id IN (SELECT id FROM pricelists WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'pricelists') THEN
        EXECUTE 'DELETE FROM pricelists WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === Quotation Templates (references products, units_of_measure) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'quotation_template_optionals') THEN
        EXECUTE 'DELETE FROM quotation_template_optionals WHERE template_id IN (SELECT id FROM quotation_templates WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'quotation_template_lines') THEN
        EXECUTE 'DELETE FROM quotation_template_lines WHERE template_id IN (SELECT id FROM quotation_templates WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'quotation_templates') THEN
        EXECUTE 'DELETE FROM quotation_templates WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === Sales Quotations (references products, contacts) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'sales_quotation_items') THEN
        EXECUTE 'DELETE FROM sales_quotation_items WHERE quotation_id IN (SELECT id FROM sales_quotations WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'sales_quotations') THEN
        EXECUTE 'DELETE FROM sales_quotations WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === Sales Returns (references products, contacts) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'sales_return_items') THEN
        EXECUTE 'DELETE FROM sales_return_items WHERE return_id IN (SELECT id FROM sales_returns WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'sales_returns') THEN
        EXECUTE 'DELETE FROM sales_returns WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === Discounts (references contacts) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'discount_usage') THEN
        EXECUTE 'DELETE FROM discount_usage WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === Customer Followups (references contacts) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'payment_promises') THEN
        EXECUTE 'DELETE FROM payment_promises WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'followup_actions') THEN
        EXECUTE 'DELETE FROM followup_actions WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'customer_followup_status') THEN
        EXECUTE 'DELETE FROM customer_followup_status WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === Reconciliation Acts (references contacts) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'reconciliation_act_lines') THEN
        EXECUTE 'DELETE FROM reconciliation_act_lines WHERE act_id IN (SELECT id FROM reconciliation_acts WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'reconciliation_acts') THEN
        EXECUTE 'DELETE FROM reconciliation_acts WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === Landed Costs (references products, contacts) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'landed_cost_allocations') THEN
        EXECUTE 'DELETE FROM landed_cost_allocations WHERE landed_cost_id IN (SELECT id FROM landed_costs WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'landed_cost_lines') THEN
        EXECUTE 'DELETE FROM landed_cost_lines WHERE landed_cost_id IN (SELECT id FROM landed_costs WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'landed_costs') THEN
        EXECUTE 'DELETE FROM landed_costs WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === Blanket Orders (references products, contacts, warehouses) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'blanket_order_release_lines') THEN
        EXECUTE 'DELETE FROM blanket_order_release_lines WHERE release_id IN (SELECT id FROM blanket_order_releases WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'blanket_order_releases') THEN
        EXECUTE 'DELETE FROM blanket_order_releases WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'blanket_order_lines') THEN
        EXECUTE 'DELETE FROM blanket_order_lines WHERE order_id IN (SELECT id FROM blanket_orders WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'blanket_orders') THEN
        EXECUTE 'DELETE FROM blanket_orders WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === RFQs / Procurement (references products, contacts) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'rfq_responses') THEN
        EXECUTE 'DELETE FROM rfq_responses WHERE rfq_id IN (SELECT id FROM rfqs WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'rfq_invitations') THEN
        EXECUTE 'DELETE FROM rfq_invitations WHERE rfq_id IN (SELECT id FROM rfqs WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'rfq_items') THEN
        EXECUTE 'DELETE FROM rfq_items WHERE rfq_id IN (SELECT id FROM rfqs WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'rfqs') THEN
        EXECUTE 'DELETE FROM rfqs WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'procurement_contract_items') THEN
        EXECUTE 'DELETE FROM procurement_contract_items WHERE contract_id IN (SELECT id FROM procurement_contracts WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'procurement_contracts') THEN
        EXECUTE 'DELETE FROM procurement_contracts WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === Supplier / Vendor (references products, contacts) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'vendor_prices') THEN
        EXECUTE 'DELETE FROM vendor_prices WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'supplier_price_history') THEN
        EXECUTE 'DELETE FROM supplier_price_history WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'supplier_performance') THEN
        EXECUTE 'DELETE FROM supplier_performance WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === Product Variants & Packagings (references products) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'product_variants') THEN
        EXECUTE 'DELETE FROM product_variants WHERE product_id IN (SELECT id FROM products WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'product_template_attributes') THEN
        EXECUTE 'DELETE FROM product_template_attributes WHERE product_id IN (SELECT id FROM products WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'product_packagings') THEN
        EXECUTE 'DELETE FROM product_packagings WHERE product_id IN (SELECT id FROM products WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'package_contents') THEN
        EXECUTE 'DELETE FROM package_contents WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === CRM (references contacts) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'quotation_lines') THEN
        EXECUTE 'DELETE FROM quotation_lines WHERE quotation_id IN (SELECT id FROM quotations WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'quotations') THEN
        EXECUTE 'DELETE FROM quotations WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'email_messages') THEN
        EXECUTE 'DELETE FROM email_messages WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'segment_members') THEN
        EXECUTE 'DELETE FROM segment_members WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'campaign_members') THEN
        EXECUTE 'DELETE FROM campaign_members WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'crm_tasks') THEN
        EXECUTE 'DELETE FROM crm_tasks WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'activities') THEN
        EXECUTE 'DELETE FROM activities WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'opportunities') THEN
        EXECUTE 'DELETE FROM opportunities WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'call_logs') THEN
        EXECUTE 'DELETE FROM call_logs WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'leads') THEN
        EXECUTE 'DELETE FROM leads WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === Projects / Expenses (references contacts) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'project_expenses') THEN
        EXECUTE 'DELETE FROM project_expenses WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'projects') THEN
        EXECUTE 'DELETE FROM projects WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'expenses') THEN
        EXECUTE 'DELETE FROM expenses WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'contracts') THEN
        EXECUTE 'DELETE FROM contracts WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === Stock moves / Warehouse operations (references products, warehouses) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'stock_moves') THEN
        EXECUTE 'DELETE FROM stock_moves WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'warehouse_steps') THEN
        EXECUTE 'DELETE FROM warehouse_steps WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === BOM (references products) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'bom_operations') THEN
        EXECUTE 'DELETE FROM bom_operations WHERE bom_id IN (SELECT id FROM product_boms WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'bom_lines') THEN
        EXECUTE 'DELETE FROM bom_lines WHERE bom_id IN (SELECT id FROM product_boms WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'product_boms') THEN
        EXECUTE 'DELETE FROM product_boms WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === Scrap / Reorder (references products, warehouses) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'scrap_orders') THEN
        EXECUTE 'DELETE FROM scrap_orders WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'reorder_rules') THEN
        EXECUTE 'DELETE FROM reorder_rules WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'reorder_alerts') THEN
        EXECUTE 'DELETE FROM reorder_alerts WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === Inventory (references products, warehouses) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'stock_count_lines') THEN
        EXECUTE 'DELETE FROM stock_count_lines WHERE stock_count_id IN (SELECT id FROM stock_counts WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'stock_counts') THEN
        EXECUTE 'DELETE FROM stock_counts WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'transfer_order_lines') THEN
        EXECUTE 'DELETE FROM transfer_order_lines WHERE transfer_order_id IN (SELECT id FROM transfer_orders WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'transfer_orders') THEN
        EXECUTE 'DELETE FROM transfer_orders WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'inventory_lots') THEN
        EXECUTE 'DELETE FROM inventory_lots WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === Recurring Journal Templates (references journals, accounts, contacts) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'recurring_journal_template_lines') THEN
        EXECUTE 'DELETE FROM recurring_journal_template_lines WHERE template_id IN (SELECT id FROM recurring_journal_templates WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'recurring_journal_templates') THEN
        EXECUTE 'DELETE FROM recurring_journal_templates WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === Core financial / order tables (always exist) ===
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

    -- === Cash (references accounts, contacts) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'cash_transactions') THEN
        EXECUTE 'DELETE FROM cash_transactions WHERE tenant_id = $1' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'cash_orders') THEN
        EXECUTE 'DELETE FROM cash_orders WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === Bank (references bank_accounts → accounts) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'bank_reconciliation_lines') THEN
        EXECUTE 'DELETE FROM bank_reconciliation_lines WHERE reconciliation_id IN (SELECT id FROM bank_reconciliations WHERE bank_account_id IN (SELECT id FROM bank_accounts WHERE tenant_id = $1))' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'bank_reconciliations') THEN
        EXECUTE 'DELETE FROM bank_reconciliations WHERE bank_account_id IN (SELECT id FROM bank_accounts WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'bank_transactions') THEN
        EXECUTE 'DELETE FROM bank_transactions WHERE bank_account_id IN (SELECT id FROM bank_accounts WHERE tenant_id = $1)' USING v_tid;
    END IF;

    -- === Budget (references accounts) ===
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'budget_lines') THEN
        EXECUTE 'DELETE FROM budget_lines WHERE budget_id IN (SELECT id FROM budgets WHERE tenant_id = $1)' USING v_tid;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'budgets') THEN
        EXECUTE 'DELETE FROM budgets WHERE tenant_id = $1' USING v_tid;
    END IF;

    -- === Contact persons (references contacts) ===
    DELETE FROM contact_persons WHERE contact_id IN (SELECT id FROM contacts WHERE tenant_id = v_tid);

    -- === Base tables ===
    DELETE FROM products WHERE tenant_id = v_tid;
    DELETE FROM product_categories WHERE tenant_id = v_tid;
    DELETE FROM bank_accounts WHERE tenant_id = v_tid;
    DELETE FROM payment_methods WHERE tenant_id = v_tid;
    DELETE FROM contacts WHERE tenant_id = v_tid;
    DELETE FROM units_of_measure WHERE tenant_id = v_tid;
    DELETE FROM journals WHERE tenant_id = v_tid AND code IN ('GEN', 'SAL', 'PUR', 'CSH', 'BNK');

    -- Reset account balances
    UPDATE accounts SET current_balance = 0, updated_at = NOW() WHERE tenant_id = v_tid;
    RAISE NOTICE 'Cleanup complete - existing demo data removed.';
END;
$$;

DO $$
DECLARE
    v_tenant_id UUID := '554423dd-5013-40a5-b3cf-1c800c2b139c';
    v_org_id UUID := 'f196eea8-46a8-4836-bee9-96dd9fa3ebb7';
    v_user_id UUID := '4eb96df3-5f2c-46e0-b270-8b90fdeb813e';

    -- Currency IDs (global)
    cur_uzs UUID := 'b3b33ee7-2d54-4767-acfc-8814367e3688';
    cur_usd UUID := '79365d91-abfd-4464-85bd-7eaeeaaa2e8b';

    -- Existing Account IDs (from registration)
    acc_cash UUID := '15dd4ccb-2a29-4f38-96ca-3cb92e6bfddb';       -- 1000 Cash
    acc_petty_cash UUID := 'd515c3ef-5fe9-4e3c-964d-fc2fb543e4bd'; -- 1010 Petty Cash
    acc_bank UUID := '302c08ec-5cd1-43ee-a8b6-02c3b6174840';       -- 1100 Bank Account
    acc_ar UUID := 'aa919cf6-5295-44ab-8fc6-08c28a74c35a';         -- 1200 Accounts Receivable
    acc_inv UUID := '1c5db4ae-591d-4165-9d39-ce3f483a7a85';        -- 1300 Inventory
    acc_inv_raw UUID := '8e7889ca-a944-4e67-81c3-ce6d6b2637bd';    -- 1310 Raw Materials
    acc_inv_finished UUID := '49bd5e5e-f8df-441d-996a-d138335f74ea'; -- 1330 Finished Goods
    acc_prepaid UUID := 'cc1411ae-7a54-4f4d-ad8b-cc5a86c4b90a';   -- 1400 Prepaid Expenses
    acc_fa UUID := '21643bff-c60f-4958-a88b-e294df821a45';         -- 1500 Fixed Assets
    acc_accum_depr UUID := 'f843ceee-f170-4614-b1e6-9887fcd4ee1c'; -- 1510 Accumulated Depreciation
    acc_ap UUID := '4f5b4690-c0bd-427d-b0f6-53849cc63bdb';         -- 2000 Accounts Payable
    acc_tax_payable UUID := 'cab44261-f95c-4823-97e7-afd3eb17e4e0'; -- 2200 Tax Payable
    acc_vat_payable UUID := '26330bd8-414c-4e6e-b56f-c928ea9a66b0'; -- 2210 VAT Payable
    acc_wages_payable UUID := '137fefb2-582b-4da9-aa42-51d8331f5327'; -- 2110 Wages Payable
    acc_unearned UUID := '629725f7-7ccc-4890-93e9-e9169e23afdc';   -- 2300 Unearned Revenue
    acc_lt_loan UUID := '22ca7c0d-10ec-4749-9da1-93f0fa828cfd';   -- 2500 Long-term Loans
    acc_equity UUID := '3990af86-c37d-4c9b-a28a-a94b7adc636a';     -- 3000 Owner's Equity
    acc_share_cap UUID := '4bb2fa5a-d1ac-4f7c-a4f9-7d03fc050693';  -- 3100 Share Capital
    acc_retained UUID := 'f5e7be8b-8392-4b70-adb7-3dc17746f9c0';   -- 3200 Retained Earnings
    acc_sales_rev UUID := '6266a6cd-4645-42b1-9a86-7128bb09676a';  -- 4000 Sales Revenue
    acc_service_rev UUID := '4da6446d-e1b6-4ad6-aabe-3418663d4418'; -- 4100 Service Revenue
    acc_other_inc UUID := '2bf9d2fb-33f0-4bc8-8c9d-b872dec7a131';  -- 4900 Other Income
    acc_cogs UUID := '04283806-e240-4b7b-9e35-ac6f039be693';       -- 5000 Cost of Goods Sold
    acc_salaries UUID := 'bc6a83b2-9037-4539-953a-07e20db1d0dd';   -- 6000 Salaries & Wages
    acc_rent UUID := 'bcba1849-5ba4-49ea-abee-93bf44ce304d';       -- 6100 Rent Expense
    acc_utilities UUID := '2840efbd-f8d5-4596-b20b-abed2e2e6358';  -- 6200 Utilities
    acc_office UUID := '9db77ca9-cab4-4f2c-ae1e-c6caa641b44f';     -- 6300 Office Supplies
    acc_depr_exp UUID := '14992275-0c21-494a-9ff9-b3d2281cbbc8';   -- 6500 Depreciation Expense
    acc_marketing UUID := '6f983f2d-7919-4ace-9d82-04fa14afab6a';  -- 6600 Advertising & Marketing
    acc_travel UUID := '13428cf8-79e7-40fc-a141-3b2630adf948';     -- 6800 Travel & Entertainment
    acc_interest_exp UUID := 'a1b3e6cb-a73e-499e-a003-e6e24ca2d915'; -- 7000 Interest Expense
    acc_bank_fees UUID := '509d0b45-44f0-4bbd-8e27-ed2dd9d4e748';  -- 7100 Bank Charges

    -- Journal IDs (will be created by seed since deployment is missing them)
    j_general UUID := gen_random_uuid();
    j_sales UUID := gen_random_uuid();
    j_purchase UUID := gen_random_uuid();
    j_cash UUID := gen_random_uuid();
    j_bank UUID := gen_random_uuid();

    -- Existing Warehouse
    wh_main UUID := '1e88750a-7a33-4100-a42f-c7bb6d17271f';

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
    wl_stock UUID := 'c0802022-1003-42aa-93b1-ad11e638dd32';

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
        "projects": {"view": true, "create": true, "delete": true, "update": true},
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
    -- 3b. JOURNALS (deployment is missing standard journals)
    -- ============================================================================
    INSERT INTO journals (id, tenant_id, code, name, type, is_active, created_at, updated_at) VALUES
    (j_general,  v_tenant_id, 'GEN', 'General Journal',  'general',  true, NOW(), NOW()),
    (j_sales,    v_tenant_id, 'SAL', 'Sales Journal',    'sale',     true, NOW(), NOW()),
    (j_purchase, v_tenant_id, 'PUR', 'Purchase Journal', 'purchase', true, NOW(), NOW()),
    (j_cash,     v_tenant_id, 'CSH', 'Cash Journal',     'cash',     true, NOW(), NOW()),
    (j_bank,     v_tenant_id, 'BNK', 'Bank Journal',     'bank',     true, NOW(), NOW());

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

COMMIT;
