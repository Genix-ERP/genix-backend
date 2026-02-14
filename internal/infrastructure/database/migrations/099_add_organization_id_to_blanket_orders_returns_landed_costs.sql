-- Migration 097: Add organization_id to blanket_orders, purchase_returns, sales_returns, landed_costs

-- blanket_orders
ALTER TABLE blanket_orders ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
CREATE INDEX IF NOT EXISTS idx_blanket_orders_organization ON blanket_orders(organization_id);

-- blanket_order_releases (child of blanket_orders)
ALTER TABLE blanket_order_releases ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
CREATE INDEX IF NOT EXISTS idx_blanket_order_releases_organization ON blanket_order_releases(organization_id);

-- purchase_returns
ALTER TABLE purchase_returns ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
CREATE INDEX IF NOT EXISTS idx_purchase_returns_organization ON purchase_returns(organization_id);

-- sales_returns
ALTER TABLE sales_returns ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
CREATE INDEX IF NOT EXISTS idx_sales_returns_organization ON sales_returns(organization_id);

-- landed_costs
ALTER TABLE landed_costs ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
CREATE INDEX IF NOT EXISTS idx_landed_costs_organization ON landed_costs(organization_id);

-- landed_cost_types
ALTER TABLE landed_cost_types ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
CREATE INDEX IF NOT EXISTS idx_landed_cost_types_organization ON landed_cost_types(organization_id);

-- Backfill blanket_orders from vendor's organization_id
UPDATE blanket_orders bo SET organization_id = (
    SELECT c.organization_id FROM contacts c WHERE c.id = bo.vendor_id
) WHERE bo.organization_id IS NULL AND bo.vendor_id IS NOT NULL;

-- Backfill blanket_orders fallback to tenant's first organization
UPDATE blanket_orders bo SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = bo.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE bo.organization_id IS NULL;

-- Backfill blanket_order_releases from their parent blanket_order
UPDATE blanket_order_releases bor SET organization_id = (
    SELECT bo.organization_id FROM blanket_orders bo WHERE bo.id = bor.blanket_order_id
) WHERE bor.organization_id IS NULL AND bor.blanket_order_id IS NOT NULL;

-- Backfill blanket_order_releases fallback
UPDATE blanket_order_releases bor SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = bor.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE bor.organization_id IS NULL;

-- Backfill purchase_returns from supplier's organization_id
UPDATE purchase_returns pr SET organization_id = (
    SELECT c.organization_id FROM contacts c WHERE c.id = pr.supplier_id
) WHERE pr.organization_id IS NULL AND pr.supplier_id IS NOT NULL;

-- Backfill purchase_returns fallback
UPDATE purchase_returns pr SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = pr.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE pr.organization_id IS NULL;

-- Backfill sales_returns from customer's organization_id
UPDATE sales_returns sr SET organization_id = (
    SELECT c.organization_id FROM contacts c WHERE c.id = sr.customer_id
) WHERE sr.organization_id IS NULL AND sr.customer_id IS NOT NULL;

-- Backfill sales_returns fallback
UPDATE sales_returns sr SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = sr.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE sr.organization_id IS NULL;

-- Backfill landed_costs from goods_receipt's organization_id
UPDATE landed_costs lc SET organization_id = (
    SELECT gr.organization_id FROM goods_receipts gr WHERE gr.id = lc.goods_receipt_id
) WHERE lc.organization_id IS NULL AND lc.goods_receipt_id IS NOT NULL;

-- Backfill landed_costs fallback
UPDATE landed_costs lc SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = lc.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE lc.organization_id IS NULL;

-- Backfill landed_cost_types fallback (no FK parent)
UPDATE landed_cost_types lct SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = lct.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE lct.organization_id IS NULL;
