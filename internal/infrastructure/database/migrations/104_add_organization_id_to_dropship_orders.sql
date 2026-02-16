-- Migration 102: Add organization_id to dropship_orders

ALTER TABLE dropship_orders ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
CREATE INDEX IF NOT EXISTS idx_dropship_orders_organization ON dropship_orders(organization_id);

-- Backfill from sales order's organization_id
UPDATE dropship_orders d SET organization_id = (
    SELECT so.organization_id FROM sales_orders so WHERE so.id = d.sales_order_id
) WHERE d.organization_id IS NULL AND d.sales_order_id IS NOT NULL;

-- Fallback to tenant's first organization
UPDATE dropship_orders d SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = d.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE d.organization_id IS NULL;
