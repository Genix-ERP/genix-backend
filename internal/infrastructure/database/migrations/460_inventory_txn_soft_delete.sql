-- 460_inventory_txn_soft_delete.sql — Ishlab chiqarish v2 follow-up fixes
--
-- 1. inventory_transactions.deleted_at. The v2 manufacturing flows
--    soft-delete ledger rows (DeleteProductionOrder cascade) and every
--    new idempotency guard filters on live rows (deleted_at IS NULL) —
--    but the column never existed. Each such query failed with 42703,
--    the error was swallowed by the Scan(...) pattern, and the guards
--    read as zero: the cancel reversal never ran, pause→resume could
--    double-issue, the material-return guard was dead and the delete
--    cascade reversed nothing.
-- 2. stock_ledger view: recreated verbatim from 447_inventory_v2.sql §4
--    with the soft-delete filter appended, so ledger reporting no longer
--    counts rows a production-order delete has reversed.
-- 3. RBAC lockout repair: 459 seeded the manufacturing:equipment and
--    manufacturing:cost_calculations permission rows, and handler.go now
--    gates those routes on them — but existing roles were never GRANTED
--    the new permissions, so role-based users started 403-ing on the
--    whole Equipment tab. Grant each new permission to every role that
--    already holds the matching manufacturing:work_centers action (the
--    gate those routes used before).

-- ---------------------------------------------------------------
-- 1. Soft-delete column + partial index
-- ---------------------------------------------------------------
ALTER TABLE inventory_transactions ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_invtxn_deleted
    ON inventory_transactions(deleted_at) WHERE deleted_at IS NOT NULL;

-- ---------------------------------------------------------------
-- 2. stock_ledger view — identical to 447 §4, plus the live-row filter.
--    Writers store outbound rows either as negative quantities or as
--    positive quantities with an outbound type; -ABS() collapses both
--    conventions. 'transfer' pairs keep their stored sign (one leg
--    negative, one positive).
-- ---------------------------------------------------------------
CREATE OR REPLACE VIEW stock_ledger AS
SELECT
    t.id,
    t.tenant_id,
    COALESCE(t.organization_id, w.organization_id)  AS organization_id,
    COALESCE(t.product_id, inv.product_id)          AS product_id,
    COALESCE(t.warehouse_id, inv.warehouse_id)      AS warehouse_id,
    t.inventory_id,
    t.transaction_type,
    t.reference_type,
    t.reference_id,
    CASE
        WHEN t.transaction_type IN (
            'issue', 'sale', 'ship', 'delivery',
            'adjustment_out', 'transfer_out',
            'consume', 'production_out', 'write_off', 'scrap'
        ) THEN -ABS(t.quantity)
        ELSE t.quantity
    END                                             AS qty_delta,
    t.unit_cost,
    t.total_cost,
    t.reason,
    t.notes,
    t.transaction_date,
    t.created_by,
    t.created_at
FROM inventory_transactions t
LEFT JOIN inventory  inv ON inv.id = t.inventory_id
LEFT JOIN warehouses w   ON w.id  = COALESCE(t.warehouse_id, inv.warehouse_id)
WHERE t.deleted_at IS NULL;

-- ---------------------------------------------------------------
-- 3. Grant the 459-seeded permissions to roles that already hold the
--    matching work_centers action (role_permissions PK = role_id +
--    permission_id, granted_at defaults).
-- ---------------------------------------------------------------
INSERT INTO role_permissions (role_id, permission_id)
SELECT rp.role_id, np.id
FROM role_permissions rp
JOIN permissions wp ON wp.id = rp.permission_id
    AND wp.module = 'manufacturing' AND wp.resource = 'work_centers'
JOIN permissions np ON np.module = 'manufacturing'
    AND np.resource IN ('equipment', 'cost_calculations')
    AND np.action = wp.action
ON CONFLICT DO NOTHING;
