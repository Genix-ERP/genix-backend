-- Fix duplicate COGS journal entry lines
-- Where both delivery (stock_operation) and sales_invoice posted COGS for the same sales order
--
-- Step 1: Review what will be affected (run this first, DO NOT skip)
SELECT
    si.invoice_number,
    so.order_number,
    sop.name as delivery_name,
    je_inv.id as invoice_je_id,
    inv_cogs.line_id as cogs_debit_line_id,
    inv_cogs.debit_amount as invoice_cogs_amount,
    inv_interim.line_id as interim_credit_line_id,
    inv_interim.credit_amount as interim_credit_amount,
    del_cogs.debit_amount as delivery_cogs_amount
FROM journal_entries je_inv
JOIN sales_invoices si ON si.id = je_inv.source_id AND je_inv.source_type = 'sales_invoice'
JOIN sales_orders so ON so.id = si.sales_order_id
JOIN stock_operations sop ON sop.source_id = so.id AND sop.source_type = 'sales_order'
    AND sop.direction = 'delivery' AND sop.state = 'done'
JOIN journal_entries je_del ON je_del.source_id = sop.id AND je_del.source_type = 'stock_operation'
    AND je_del.status = 'posted' AND je_del.deleted_at IS NULL
-- Invoice has COGS debit line (5xxx account)
CROSS JOIN LATERAL (
    SELECT jel.id as line_id, jel.debit_amount
    FROM journal_entry_lines jel
    JOIN accounts a ON a.id = jel.account_id
    WHERE jel.journal_entry_id = je_inv.id AND a.code LIKE '5%' AND jel.debit_amount > 0
    LIMIT 1
) inv_cogs
-- Invoice has Stock Interim credit line (2231 or similar)
LEFT JOIN LATERAL (
    SELECT jel.id as line_id, jel.credit_amount
    FROM journal_entry_lines jel
    JOIN accounts a ON a.id = jel.account_id
    WHERE jel.journal_entry_id = je_inv.id AND a.code LIKE '22%' AND jel.credit_amount > 0
    LIMIT 1
) inv_interim ON true
-- Delivery has COGS debit
CROSS JOIN LATERAL (
    SELECT SUM(jel.debit_amount) as debit_amount
    FROM journal_entry_lines jel
    JOIN accounts a ON a.id = jel.account_id
    WHERE jel.journal_entry_id = je_del.id AND a.code LIKE '5%' AND jel.debit_amount > 0
) del_cogs
WHERE je_inv.status = 'posted' AND je_inv.deleted_at IS NULL
ORDER BY si.invoice_number;

-- Step 2: If the above looks correct, run the fix inside a transaction:
-- BEGIN;
--
-- -- Delete duplicate COGS debit lines (5xxx) from invoice journal entries
-- DELETE FROM journal_entry_lines WHERE id IN (
--     SELECT inv_cogs.line_id
--     FROM journal_entries je_inv
--     JOIN sales_invoices si ON si.id = je_inv.source_id AND je_inv.source_type = 'sales_invoice'
--     JOIN sales_orders so ON so.id = si.sales_order_id
--     JOIN stock_operations sop ON sop.source_id = so.id AND sop.source_type = 'sales_order'
--         AND sop.direction = 'delivery' AND sop.state = 'done'
--     JOIN journal_entries je_del ON je_del.source_id = sop.id AND je_del.source_type = 'stock_operation'
--         AND je_del.status = 'posted' AND je_del.deleted_at IS NULL
--     CROSS JOIN LATERAL (
--         SELECT jel.id as line_id, jel.debit_amount
--         FROM journal_entry_lines jel
--         JOIN accounts a ON a.id = jel.account_id
--         WHERE jel.journal_entry_id = je_inv.id AND a.code LIKE '5%' AND jel.debit_amount > 0
--     ) inv_cogs
--     WHERE je_inv.status = 'posted' AND je_inv.deleted_at IS NULL
-- );
--
-- -- Delete matching Stock Interim credit lines (22xx) from same invoice journal entries
-- DELETE FROM journal_entry_lines WHERE id IN (
--     SELECT inv_interim.line_id
--     FROM journal_entries je_inv
--     JOIN sales_invoices si ON si.id = je_inv.source_id AND je_inv.source_type = 'sales_invoice'
--     JOIN sales_orders so ON so.id = si.sales_order_id
--     JOIN stock_operations sop ON sop.source_id = so.id AND sop.source_type = 'sales_order'
--         AND sop.direction = 'delivery' AND sop.state = 'done'
--     JOIN journal_entries je_del ON je_del.source_id = sop.id AND je_del.source_type = 'stock_operation'
--         AND je_del.status = 'posted' AND je_del.deleted_at IS NULL
--     CROSS JOIN LATERAL (
--         SELECT jel.id as line_id
--         FROM journal_entry_lines jel
--         JOIN accounts a ON a.id = jel.account_id
--         WHERE jel.journal_entry_id = je_inv.id AND a.code LIKE '22%' AND jel.credit_amount > 0
--     ) inv_interim
--     WHERE je_inv.status = 'posted' AND je_inv.deleted_at IS NULL
-- );
--
-- -- Recalculate journal entry totals from remaining lines
-- UPDATE journal_entries je SET
--     total_debit = sub.actual_debit,
--     total_credit = sub.actual_credit,
--     updated_at = NOW()
-- FROM (
--     SELECT je2.id, COALESCE(SUM(jel.debit_amount), 0) as actual_debit, COALESCE(SUM(jel.credit_amount), 0) as actual_credit
--     FROM journal_entries je2
--     JOIN sales_invoices si ON si.id = je2.source_id AND je2.source_type = 'sales_invoice'
--     JOIN sales_orders so ON so.id = si.sales_order_id
--     JOIN stock_operations sop ON sop.source_id = so.id AND sop.source_type = 'sales_order'
--         AND sop.direction = 'delivery' AND sop.state = 'done'
--     JOIN journal_entries je_del ON je_del.source_id = sop.id AND je_del.source_type = 'stock_operation'
--         AND je_del.status = 'posted' AND je_del.deleted_at IS NULL
--     LEFT JOIN journal_entry_lines jel ON jel.journal_entry_id = je2.id
--     WHERE je2.status = 'posted' AND je2.deleted_at IS NULL
--     GROUP BY je2.id
-- ) sub
-- WHERE je.id = sub.id;
--
-- COMMIT;
