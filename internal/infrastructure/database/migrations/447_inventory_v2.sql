-- 447_inventory_v2.sql
--
-- Ombor (Inventory) module rebuild — data-integrity phase.
-- Full background: docs/ombor-audit.md (repo root /docs).
--
-- What this migration does:
--   1. Makes the movement ledger self-contained: inventory_transactions
--      gains product_id / warehouse_id (backfilled from the inventory
--      row it points at). Until now the ledger keyed only on
--      inventory_id, so a deleted/merged balance row orphaned its
--      history and the ledger could not rebuild per-warehouse on-hand.
--   2. transaction_type CHECK backstop, NOT VALID — enforced for new
--      rows only; historical rows keep whatever free-typed values the
--      ~25 writer paths produced (see audit §1).
--   3. tenant_id on warehouse_locations and stock_count_lines
--      (backfilled from parent warehouse / stock count). Closes the
--      cross-tenant read in warehouses.go location-delete guard.
--   4. stock_ledger view — THE single signed-delta normalization
--      (previously copy-pasted CASE lists in stockAtDateQuery and
--      turnover). All new reporting reads this view.
--   5. Ledger replay + dashboard indexes.
--
-- Companion Go changes: internal/handler/inventory_stats.go (new
-- GET /inventory/stats), inventory.go (stock-op atomicity + internal
-- transfer stock moves + weighted valuation), pos.go (repaired stock
-- SQL + consumption JE), sales.go (transactional ship), goods_receipts.go
-- / purchase_orders.go (receipt dedupe guards), ai_agent_tools.go
-- (correct ledger writes + events), warehouse_alerts.go (dead queries),
-- workflow_engine.go (low_stock Scheduled flag).

-- ---------------------------------------------------------------
-- 1. Self-contained ledger
-- ---------------------------------------------------------------
ALTER TABLE inventory_transactions ADD COLUMN IF NOT EXISTS product_id UUID REFERENCES products(id);
ALTER TABLE inventory_transactions ADD COLUMN IF NOT EXISTS warehouse_id UUID REFERENCES warehouses(id);

UPDATE inventory_transactions t
SET product_id   = COALESCE(t.product_id, inv.product_id),
    warehouse_id = COALESCE(t.warehouse_id, inv.warehouse_id)
FROM inventory inv
WHERE inv.id = t.inventory_id
  AND (t.product_id IS NULL OR t.warehouse_id IS NULL);

CREATE INDEX IF NOT EXISTS idx_invtxn_tenant_product_wh
    ON inventory_transactions (tenant_id, product_id, warehouse_id);
CREATE INDEX IF NOT EXISTS idx_invtxn_tenant_date
    ON inventory_transactions (tenant_id, transaction_date DESC);
CREATE INDEX IF NOT EXISTS idx_invtxn_reference
    ON inventory_transactions (reference_type, reference_id);

-- ---------------------------------------------------------------
-- 2. transaction_type backstop (new rows only — history untouched)
-- ---------------------------------------------------------------
ALTER TABLE inventory_transactions DROP CONSTRAINT IF EXISTS chk_invtxn_type;
ALTER TABLE inventory_transactions ADD CONSTRAINT chk_invtxn_type CHECK (
    transaction_type IN (
        'receipt', 'issue', 'transfer', 'transfer_in', 'transfer_out',
        'adjustment', 'adjustment_in', 'adjustment_out', 'count',
        'sale', 'ship', 'delivery', 'consume', 'return',
        'production_in', 'production_out', 'production_complete', 'production_scrap',
        'write_off', 'scrap', 'opening', 'landed_cost'
    )
) NOT VALID;

-- ---------------------------------------------------------------
-- 3. tenant_id gaps
-- ---------------------------------------------------------------
ALTER TABLE warehouse_locations ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenants(id) ON DELETE CASCADE;
UPDATE warehouse_locations wl
SET tenant_id = w.tenant_id
FROM warehouses w
WHERE w.id = wl.warehouse_id AND wl.tenant_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_warehouse_locations_tenant ON warehouse_locations (tenant_id);

ALTER TABLE stock_count_lines ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenants(id) ON DELETE CASCADE;
UPDATE stock_count_lines l
SET tenant_id = c.tenant_id
FROM stock_counts c
WHERE c.id = l.stock_count_id AND l.tenant_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_stock_count_lines_tenant ON stock_count_lines (tenant_id);

-- ---------------------------------------------------------------
-- 4. stock_ledger view — the one place signs are normalized.
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
LEFT JOIN warehouses w   ON w.id  = COALESCE(t.warehouse_id, inv.warehouse_id);
