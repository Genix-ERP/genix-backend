-- Migration 085: Add organization_id to remaining tables for multi-org data isolation
-- Tables: products, product_categories, product_boms, inventory, inventory_transactions,
--         reorder_rules, scrap_orders, work_centers, production_orders, work_orders,
--         quality_checks, opportunities, bank_transactions, warehouse_operation_types

-- =====================================================
-- PRODUCTS & INVENTORY
-- =====================================================

-- products
ALTER TABLE products ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
UPDATE products p SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = p.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE p.organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_products_organization ON products(organization_id);

-- product_categories
ALTER TABLE product_categories ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
UPDATE product_categories pc SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = pc.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE pc.organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_product_categories_organization ON product_categories(organization_id);

-- product_boms (backfill from products via product_id)
ALTER TABLE product_boms ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
UPDATE product_boms pb SET organization_id = (
    SELECT p.organization_id FROM products p WHERE p.id = pb.product_id
) WHERE pb.organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_product_boms_organization ON product_boms(organization_id);

-- inventory (backfill from warehouses via warehouse_id)
ALTER TABLE inventory ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
UPDATE inventory i SET organization_id = (
    SELECT w.organization_id FROM warehouses w WHERE w.id = i.warehouse_id
) WHERE i.organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_inventory_organization ON inventory(organization_id);

-- inventory_transactions (backfill from inventory via inventory_id)
ALTER TABLE inventory_transactions ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
UPDATE inventory_transactions it SET organization_id = (
    SELECT inv.organization_id FROM inventory inv WHERE inv.id = it.inventory_id
) WHERE it.organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_inventory_transactions_organization ON inventory_transactions(organization_id);

-- reorder_rules (backfill from warehouses via warehouse_id)
ALTER TABLE reorder_rules ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
UPDATE reorder_rules r SET organization_id = (
    SELECT w.organization_id FROM warehouses w WHERE w.id = r.warehouse_id
) WHERE r.organization_id IS NULL AND r.warehouse_id IS NOT NULL;
UPDATE reorder_rules r SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = r.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE r.organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_reorder_rules_organization ON reorder_rules(organization_id);

-- scrap_orders (backfill from warehouses via warehouse_id)
ALTER TABLE scrap_orders ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
UPDATE scrap_orders s SET organization_id = (
    SELECT w.organization_id FROM warehouses w WHERE w.id = s.warehouse_id
) WHERE s.organization_id IS NULL AND s.warehouse_id IS NOT NULL;
UPDATE scrap_orders s SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = s.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE s.organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_scrap_orders_organization ON scrap_orders(organization_id);

-- =====================================================
-- MANUFACTURING
-- =====================================================

-- work_centers (backfill from warehouses via warehouse_id)
ALTER TABLE work_centers ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
UPDATE work_centers wc SET organization_id = (
    SELECT w.organization_id FROM warehouses w WHERE w.id = wc.warehouse_id
) WHERE wc.organization_id IS NULL AND wc.warehouse_id IS NOT NULL;
UPDATE work_centers wc SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = wc.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE wc.organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_work_centers_organization ON work_centers(organization_id);

-- production_orders (backfill from warehouses via warehouse_id)
ALTER TABLE production_orders ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
UPDATE production_orders po SET organization_id = (
    SELECT w.organization_id FROM warehouses w WHERE w.id = po.warehouse_id
) WHERE po.organization_id IS NULL AND po.warehouse_id IS NOT NULL;
UPDATE production_orders po SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = po.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE po.organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_production_orders_organization ON production_orders(organization_id);

-- work_orders (backfill from production_orders via production_order_id)
ALTER TABLE work_orders ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
UPDATE work_orders wo SET organization_id = (
    SELECT po.organization_id FROM production_orders po WHERE po.id = wo.production_order_id
) WHERE wo.organization_id IS NULL AND wo.production_order_id IS NOT NULL;
UPDATE work_orders wo SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = wo.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE wo.organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_work_orders_organization ON work_orders(organization_id);

-- quality_checks (backfill from production_orders via production_order_id)
ALTER TABLE quality_checks ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
UPDATE quality_checks qc SET organization_id = (
    SELECT po.organization_id FROM production_orders po WHERE po.id = qc.production_order_id
) WHERE qc.organization_id IS NULL AND qc.production_order_id IS NOT NULL;
UPDATE quality_checks qc SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = qc.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE qc.organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_quality_checks_organization ON quality_checks(organization_id);

-- =====================================================
-- CRM
-- =====================================================

-- opportunities (backfill from contacts via contact_id)
ALTER TABLE opportunities ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
UPDATE opportunities op SET organization_id = (
    SELECT c.organization_id FROM contacts c WHERE c.id = op.contact_id
) WHERE op.organization_id IS NULL AND op.contact_id IS NOT NULL;
UPDATE opportunities op SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = op.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE op.organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_opportunities_organization ON opportunities(organization_id);

-- =====================================================
-- FINANCE
-- =====================================================

-- bank_transactions (backfill from bank_accounts via bank_account_id)
ALTER TABLE bank_transactions ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
UPDATE bank_transactions bt SET organization_id = (
    SELECT ba.organization_id FROM bank_accounts ba WHERE ba.id = bt.bank_account_id
) WHERE bt.organization_id IS NULL AND bt.bank_account_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_bank_transactions_organization ON bank_transactions(organization_id);

-- =====================================================
-- WAREHOUSE SUB-TABLES
-- =====================================================

-- warehouse_operation_types (backfill from warehouses via warehouse_id)
ALTER TABLE warehouse_operation_types ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
UPDATE warehouse_operation_types wot SET organization_id = (
    SELECT w.organization_id FROM warehouses w WHERE w.id = wot.warehouse_id
) WHERE wot.organization_id IS NULL AND wot.warehouse_id IS NOT NULL;
UPDATE warehouse_operation_types wot SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = wot.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE wot.organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_warehouse_operation_types_organization ON warehouse_operation_types(organization_id);
