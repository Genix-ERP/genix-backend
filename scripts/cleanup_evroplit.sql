-- ============================================================
-- CLEANUP SCRIPT FOR EVROPLIT ORGANIZATION
-- Keeps: products, BOMs, warehouses, work centers, accounts, contacts
-- Deletes: inventory movements, sales, purchases, manufacturing orders, finance entries
-- ============================================================
-- VERIFY ORG ID BEFORE RUNNING!

BEGIN;

SET search_path TO public;

-- The EVROPLIT organization ID
DO $$ DECLARE org UUID := '465fb035-c265-49c3-88e6-0c1762f4efdc'; BEGIN

-- 1. MANUFACTURING: delete work order children first, then work orders, then production orders
DELETE FROM work_order_time_logs WHERE work_order_id IN (
    SELECT wo.id FROM work_orders wo
    JOIN production_orders po ON wo.production_order_id = po.id
    WHERE po.organization_id = org
);
DELETE FROM work_order_materials WHERE work_order_id IN (
    SELECT wo.id FROM work_orders wo
    JOIN production_orders po ON wo.production_order_id = po.id
    WHERE po.organization_id = org
);
DELETE FROM work_orders WHERE production_order_id IN (
    SELECT id FROM production_orders WHERE organization_id = org
);
DELETE FROM production_orders WHERE organization_id = org;

-- 2. INVENTORY: transactions, lots, then reset quantities
DELETE FROM inventory_transactions WHERE organization_id = org;
DELETE FROM inventory_lots WHERE tenant_id IN (
    SELECT tenant_id FROM organizations WHERE id = org
) AND warehouse_id IN (
    SELECT id FROM warehouses WHERE organization_id = org
);
UPDATE inventory SET quantity_on_hand = 0, quantity_reserved = 0,
    last_movement_date = NULL, updated_at = NOW()
WHERE organization_id = org;

-- 3. SALES: lines cascade from parent deletes
DELETE FROM sales_delivery_order_lines WHERE delivery_order_id IN (
    SELECT id FROM sales_delivery_orders WHERE tenant_id IN (
        SELECT tenant_id FROM organizations WHERE id = org
    ) AND organization_id = org
);
DELETE FROM sales_delivery_orders WHERE organization_id = org;
DELETE FROM sales_return_items WHERE return_id IN (
    SELECT id FROM sales_returns WHERE organization_id = org
);
DELETE FROM sales_returns WHERE organization_id = org;
DELETE FROM sales_invoice_lines WHERE invoice_id IN (
    SELECT id FROM sales_invoices WHERE organization_id = org
);
DELETE FROM sales_invoices WHERE organization_id = org;
DELETE FROM sales_order_lines WHERE sales_order_id IN (
    SELECT id FROM sales_orders WHERE organization_id = org
);
-- Wipe inventory_transactions that reference these sales_orders BEFORE
-- the parent rows go. Without this we leave dangling reference_id rows
-- (production incident: 19 such orphans across 6 products bled −278.75
-- units off on-hand after one of these scripts ran). The matching
-- inventory.quantity_on_hand cache will need to be rebuilt after this
-- cleanup — see comment block at end.
DELETE FROM inventory_transactions
WHERE reference_type = 'sales_order'
  AND reference_id IN (SELECT id FROM sales_orders WHERE organization_id = org);
DELETE FROM sales_orders WHERE organization_id = org;
DELETE FROM sales_quotation_items WHERE quotation_id IN (
    SELECT id FROM sales_quotations WHERE organization_id = org
);
DELETE FROM sales_quotations WHERE organization_id = org;

-- 4. PURCHASES: lines cascade from parent deletes
DELETE FROM goods_receipt_lines WHERE receipt_id IN (
    SELECT id FROM goods_receipts WHERE organization_id = org
);
DELETE FROM goods_receipts WHERE organization_id = org;
DELETE FROM purchase_invoice_lines WHERE invoice_id IN (
    SELECT id FROM purchase_invoices WHERE organization_id = org
);
DELETE FROM purchase_invoices WHERE organization_id = org;
DELETE FROM purchase_order_lines WHERE purchase_order_id IN (
    SELECT id FROM purchase_orders WHERE organization_id = org
);
DELETE FROM purchase_orders WHERE organization_id = org;
DELETE FROM rfq_response_items WHERE response_id IN (
    SELECT id FROM rfq_responses WHERE rfq_id IN (
        SELECT id FROM rfqs WHERE organization_id = org
    )
);
DELETE FROM rfq_responses WHERE rfq_id IN (
    SELECT id FROM rfqs WHERE organization_id = org
);
DELETE FROM rfq_invitations WHERE rfq_id IN (
    SELECT id FROM rfqs WHERE organization_id = org
);
DELETE FROM rfq_items WHERE rfq_id IN (
    SELECT id FROM rfqs WHERE organization_id = org
);
DELETE FROM rfqs WHERE organization_id = org;

-- 5. FINANCE: journal entry lines, entries, payments, cash
DELETE FROM payment_allocations WHERE payment_id IN (
    SELECT id FROM payments WHERE organization_id = org
);
DELETE FROM payments WHERE organization_id = org;
DELETE FROM journal_entry_lines WHERE journal_entry_id IN (
    SELECT id FROM journal_entries WHERE organization_id = org
);
DELETE FROM journal_entries WHERE organization_id = org;
DELETE FROM cash_orders WHERE register_id IN (
    SELECT id FROM cash_registers WHERE organization_id = org
);
DELETE FROM cash_registers WHERE organization_id = org;
DELETE FROM cash_transactions WHERE organization_id = org;
DELETE FROM exchange_diffs WHERE organization_id = org;
DELETE FROM reconciliation_lines WHERE reconciliation_id IN (
    SELECT id FROM reconciliation_acts WHERE organization_id = org
);
DELETE FROM reconciliation_acts WHERE organization_id = org;
DELETE FROM budget_lines WHERE budget_id IN (
    SELECT id FROM budgets WHERE organization_id = org
);
DELETE FROM budgets WHERE organization_id = org;

-- 6. SCRAP
DELETE FROM scrap_orders WHERE tenant_id IN (
    SELECT tenant_id FROM organizations WHERE id = org
) AND organization_id = org;

-- 7. TRANSFER ORDERS
DELETE FROM transfer_order_lines WHERE transfer_order_id IN (
    SELECT id FROM transfer_orders WHERE tenant_id IN (
        SELECT tenant_id FROM organizations WHERE id = org
    ) AND (from_warehouse_id IN (SELECT id FROM warehouses WHERE organization_id = org)
        OR to_warehouse_id IN (SELECT id FROM warehouses WHERE organization_id = org))
);
DELETE FROM transfer_orders WHERE tenant_id IN (
    SELECT tenant_id FROM organizations WHERE id = org
) AND (from_warehouse_id IN (SELECT id FROM warehouses WHERE organization_id = org)
    OR to_warehouse_id IN (SELECT id FROM warehouses WHERE organization_id = org));

-- 8. Reset journal next_number counters
UPDATE journals SET next_number = 1 WHERE tenant_id IN (
    SELECT tenant_id FROM organizations WHERE id = org
) AND organization_id = org;

RAISE NOTICE 'EVROPLIT cleanup complete for org %', org;
END $$;

COMMIT;
