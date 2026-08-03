-- 449_xarid_v2.sql — Xarid (Purchasing) v2 hardening (docs/xarid-audit.md §11.1)
--
-- 1. purchase_orders.status finally gets a CHECK backstop. The column has
--    been free text since 002 — the two dedicated-endpoint blocks in
--    UpdatePurchaseOrder were the only guard. Legacy variants are
--    normalized first so the constraint can attach.
-- 2. Composite indexes for the stats endpoint / list filters: the old
--    single-column tenant and status indexes from 002 don't serve
--    (tenant, status) or (tenant, expected_date) scans.

-- Normalize legacy / frontend-dialect statuses before constraining.
UPDATE purchase_orders SET status = 'ordered'  WHERE status = 'sent';
UPDATE purchase_orders SET status = 'approved' WHERE status = 'confirmed';
UPDATE purchase_orders SET status = 'received' WHERE status IN ('completed', 'done', 'closed');
UPDATE purchase_orders SET status = 'draft'
WHERE status NOT IN ('draft', 'pending_approval', 'approved', 'ordered', 'partial', 'received', 'cancelled');

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'chk_purchase_orders_status'
    ) THEN
        ALTER TABLE purchase_orders
            ADD CONSTRAINT chk_purchase_orders_status
            CHECK (status IN ('draft', 'pending_approval', 'approved', 'ordered', 'partial', 'received', 'cancelled'));
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_purchase_orders_tenant_status
    ON purchase_orders(tenant_id, status) WHERE deleted_at IS NULL;

-- Open-pipeline scans: "Kutilmoqda", "Kechikkan yetkazmalar", the
-- delivery-overdue scheduler and the upcoming-deliveries mini-table.
CREATE INDEX IF NOT EXISTS idx_purchase_orders_tenant_expected
    ON purchase_orders(tenant_id, expected_date)
    WHERE deleted_at IS NULL AND status IN ('approved', 'ordered', 'partial');

CREATE INDEX IF NOT EXISTS idx_purchase_orders_tenant_vendor
    ON purchase_orders(tenant_id, vendor_id) WHERE deleted_at IS NULL;
