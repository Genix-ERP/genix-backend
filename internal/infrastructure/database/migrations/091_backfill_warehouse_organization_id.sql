-- Migration 089: Backfill organization_id for warehouses and dependent tables
-- Warehouses had the column since migration 002 but existing rows were never backfilled

-- Step 1: Backfill warehouses with the first organization in their tenant
UPDATE warehouses w SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = w.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE w.organization_id IS NULL;

-- Step 2: Cascade to dependent tables that were backfilled from warehouses in migration 085
-- (those got NULL because warehouses had NULL at the time)

-- inventory
UPDATE inventory i SET organization_id = (
    SELECT w.organization_id FROM warehouses w WHERE w.id = i.warehouse_id
) WHERE i.organization_id IS NULL AND i.warehouse_id IS NOT NULL;

-- inventory_transactions (uses from_warehouse_id, not warehouse_id)
UPDATE inventory_transactions it SET organization_id = (
    SELECT w.organization_id FROM warehouses w WHERE w.id = it.from_warehouse_id
) WHERE it.organization_id IS NULL AND it.from_warehouse_id IS NOT NULL;

-- reorder_rules
UPDATE reorder_rules r SET organization_id = (
    SELECT w.organization_id FROM warehouses w WHERE w.id = r.warehouse_id
) WHERE r.organization_id IS NULL AND r.warehouse_id IS NOT NULL;

-- scrap_orders
UPDATE scrap_orders s SET organization_id = (
    SELECT w.organization_id FROM warehouses w WHERE w.id = s.warehouse_id
) WHERE s.organization_id IS NULL AND s.warehouse_id IS NOT NULL;

-- work_centers
UPDATE work_centers wc SET organization_id = (
    SELECT w.organization_id FROM warehouses w WHERE w.id = wc.warehouse_id
) WHERE wc.organization_id IS NULL AND wc.warehouse_id IS NOT NULL;

-- production_orders
UPDATE production_orders po SET organization_id = (
    SELECT w.organization_id FROM warehouses w WHERE w.id = po.warehouse_id
) WHERE po.organization_id IS NULL AND po.warehouse_id IS NOT NULL;

-- work_orders (from production_orders)
UPDATE work_orders wo SET organization_id = (
    SELECT po.organization_id FROM production_orders po WHERE po.id = wo.production_order_id
) WHERE wo.organization_id IS NULL AND wo.production_order_id IS NOT NULL;

-- quality_checks (from production_orders)
UPDATE quality_checks qc SET organization_id = (
    SELECT po.organization_id FROM production_orders po WHERE po.id = qc.production_order_id
) WHERE qc.organization_id IS NULL AND qc.production_order_id IS NOT NULL;

-- warehouse_operation_types
UPDATE warehouse_operation_types wot SET organization_id = (
    SELECT w.organization_id FROM warehouses w WHERE w.id = wot.warehouse_id
) WHERE wot.organization_id IS NULL AND wot.warehouse_id IS NOT NULL;

-- goods_receipts (from migration 086, also cascaded from warehouses)
UPDATE goods_receipts gr SET organization_id = (
    SELECT w.organization_id FROM warehouses w WHERE w.id = gr.warehouse_id
) WHERE gr.organization_id IS NULL AND gr.warehouse_id IS NOT NULL;
