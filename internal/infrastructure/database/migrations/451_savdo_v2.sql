-- Migration 451: Savdo (Sales) v2 — status invariants, AR-overdue flag, dead-column backfills, stats indexes.
-- Audit: docs/savdo-audit.md (2026-08-03). Mirrors the Xarid v2 pattern (449).

-- 1. Status CHECK constraints (normalize legacy values first; 'quotation' is the de-facto
--    seventh order status the frontend already writes, keep it legal).
UPDATE sales_orders SET status = 'draft'
WHERE status NOT IN ('draft','quotation','confirmed','processing','shipped','delivered','cancelled');

ALTER TABLE sales_orders DROP CONSTRAINT IF EXISTS chk_sales_orders_status;
ALTER TABLE sales_orders ADD CONSTRAINT chk_sales_orders_status
    CHECK (status IN ('draft','quotation','confirmed','processing','shipped','delivered','cancelled'));

UPDATE sales_orders SET payment_status = 'unpaid'
WHERE payment_status IS NULL OR payment_status NOT IN ('unpaid','partial','paid');

ALTER TABLE sales_orders DROP CONSTRAINT IF EXISTS chk_sales_orders_payment_status;
ALTER TABLE sales_orders ADD CONSTRAINT chk_sales_orders_payment_status
    CHECK (payment_status IN ('unpaid','partial','paid'));

UPDATE sales_invoices SET status = 'draft'
WHERE status NOT IN ('draft','sent','partial','paid','overdue','cancelled','void');

ALTER TABLE sales_invoices DROP CONSTRAINT IF EXISTS chk_sales_invoices_status;
ALTER TABLE sales_invoices ADD CONSTRAINT chk_sales_invoices_status
    CHECK (status IN ('draft','sent','partial','paid','overdue','cancelled','void'));

UPDATE sales_delivery_orders SET status = 'draft'
WHERE status NOT IN ('draft','ready','shipped','delivered','cancelled');

ALTER TABLE sales_delivery_orders DROP CONSTRAINT IF EXISTS chk_sales_delivery_orders_status;
ALTER TABLE sales_delivery_orders ADD CONSTRAINT chk_sales_delivery_orders_status
    CHECK (status IN ('draft','ready','shipped','delivered','cancelled'));

UPDATE sales_quotations SET status = 'draft'
WHERE status NOT IN ('draft','sent','accepted','rejected','expired','cancelled');

ALTER TABLE sales_quotations DROP CONSTRAINT IF EXISTS chk_sales_quotations_status;
ALTER TABLE sales_quotations ADD CONSTRAINT chk_sales_quotations_status
    CHECK (status IN ('draft','sent','accepted','rejected','expired','cancelled'));

-- 2. One-shot AR overdue-notification flag (mirror of purchase_invoices.overdue_notified, 254).
ALTER TABLE sales_invoices ADD COLUMN IF NOT EXISTS overdue_notified BOOLEAN NOT NULL DEFAULT FALSE;

-- 3. Backfill customer_name: the overdue scanner errors out on NULL customer_name rows,
--    and only CreateInvoiceFromOrder ever wrote the column.
UPDATE sales_invoices si SET customer_name = c.name
FROM contacts c
WHERE si.customer_id = c.id AND (si.customer_name IS NULL OR si.customer_name = '');

-- 4. Backfill sales_order_lines.quantity_invoiced (dead column: INSERTed as 0, never UPDATEd).
UPDATE sales_order_lines sol SET quantity_invoiced = agg.qty
FROM (
    SELECT sil.sales_order_line_id, SUM(sil.quantity) AS qty
    FROM sales_invoice_lines sil
    JOIN sales_invoices si ON si.id = sil.sales_invoice_id
    WHERE si.deleted_at IS NULL AND si.status NOT IN ('cancelled','void')
      AND sil.sales_order_line_id IS NOT NULL
    GROUP BY sil.sales_order_line_id
) agg
WHERE sol.id = agg.sales_order_line_id;

-- 5. Indexes for GET /sales-orders/stats and the overdue scanner.
CREATE INDEX IF NOT EXISTS idx_sales_orders_tenant_status
    ON sales_orders(tenant_id, status) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_sales_orders_tenant_date
    ON sales_orders(tenant_id, order_date) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_sales_orders_tenant_customer
    ON sales_orders(tenant_id, customer_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_sales_invoices_tenant_due
    ON sales_invoices(tenant_id, due_date) WHERE deleted_at IS NULL AND status IN ('sent','partial','overdue');
CREATE INDEX IF NOT EXISTS idx_sales_invoices_tenant_customer
    ON sales_invoices(tenant_id, customer_id) WHERE deleted_at IS NULL;
