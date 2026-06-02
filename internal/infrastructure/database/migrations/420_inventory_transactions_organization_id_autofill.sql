-- Migration 420 — backfill + auto-fill organization_id on inventory_transactions
--
-- Why this exists
-- ----------------
-- The Hisobotlar (inventory reports) endpoint filters inventory_transactions
-- by organization_id when the request carries an organization context:
--
--   WHERE t.tenant_id = $1 AND t.organization_id = $orgID
--
-- Several handlers (sales_delivery.go SO-ship, sales.go, sales_returns.go,
-- purchase_returns.go, goods_receipts.go, landed_costs.go, pos.go,
-- intercompany.go) INSERT into inventory_transactions WITHOUT supplying
-- organization_id. Those rows land with organization_id = NULL and are
-- silently filtered out of every org-scoped read — most visibly the
-- "Jami chiqim" total in the Movements report, which is why several
-- products (notably ones whose chiqim comes from SO-ship like Rodbond MP-75)
-- show only kirim with no matching chiqim.
--
-- Migration 087 added the column and did a one-time backfill from
-- inventory.organization_id, but nothing keeps it populated, so every
-- SO-ship since then has accumulated NULL-org rows.
--
-- This migration:
--   1. Re-backfills organization_id from inventory.organization_id for any
--      transaction rows that still have NULL — covers everything written
--      since the original 087 backfill.
--   2. Installs a BEFORE INSERT trigger that auto-derives organization_id
--      from inventory.organization_id (or from from_warehouse_id /
--      to_warehouse_id as a fallback for the rare transfer rows where
--      inventory_id is not yet resolved). This way every future write —
--      from any handler, future or existing — is automatically correct,
--      so we don't need to chase eight INSERT statements across the
--      codebase to keep them in sync.
--
-- The trigger only fires when organization_id IS NULL on the inserted row,
-- so handlers that DO supply an explicit value (construction.go,
-- manufacturing.go, work_orders.go, purchase_orders.go, production_split.go,
-- inventory.go) keep working unchanged.

-- 1. Backfill from inventory.organization_id
UPDATE inventory_transactions it
SET organization_id = inv.organization_id
FROM inventory inv
WHERE it.inventory_id = inv.id
  AND it.organization_id IS NULL
  AND inv.organization_id IS NOT NULL;

-- 2. Fallback backfill for rows whose inventory row has NULL org but whose
--    warehouse pointers resolve to one — picks from_warehouse first
--    (outbound rows), then to_warehouse (inbound rows).
UPDATE inventory_transactions it
SET organization_id = w.organization_id
FROM warehouses w
WHERE it.from_warehouse_id = w.id
  AND it.organization_id IS NULL
  AND w.organization_id IS NOT NULL;

UPDATE inventory_transactions it
SET organization_id = w.organization_id
FROM warehouses w
WHERE it.to_warehouse_id = w.id
  AND it.organization_id IS NULL
  AND w.organization_id IS NOT NULL;

-- 3. Trigger function — auto-derive organization_id on INSERT when caller
--    didn't supply one. Cheap (one indexed lookup) and silently no-ops if
--    nothing resolves, so behavior matches today's worst case rather than
--    failing the write.
CREATE OR REPLACE FUNCTION inventory_transactions_autofill_organization_id()
RETURNS TRIGGER AS $$
BEGIN
  IF NEW.organization_id IS NULL THEN
    -- Primary source: the inventory row this transaction operates on.
    IF NEW.inventory_id IS NOT NULL THEN
      SELECT inv.organization_id INTO NEW.organization_id
      FROM inventory inv
      WHERE inv.id = NEW.inventory_id;
    END IF;

    -- Fallback 1: from_warehouse (issue / outbound)
    IF NEW.organization_id IS NULL AND NEW.from_warehouse_id IS NOT NULL THEN
      SELECT w.organization_id INTO NEW.organization_id
      FROM warehouses w
      WHERE w.id = NEW.from_warehouse_id;
    END IF;

    -- Fallback 2: to_warehouse (receipt / inbound)
    IF NEW.organization_id IS NULL AND NEW.to_warehouse_id IS NOT NULL THEN
      SELECT w.organization_id INTO NEW.organization_id
      FROM warehouses w
      WHERE w.id = NEW.to_warehouse_id;
    END IF;
  END IF;

  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_inventory_transactions_autofill_organization_id
  ON inventory_transactions;

CREATE TRIGGER trg_inventory_transactions_autofill_organization_id
BEFORE INSERT ON inventory_transactions
FOR EACH ROW
EXECUTE FUNCTION inventory_transactions_autofill_organization_id();
