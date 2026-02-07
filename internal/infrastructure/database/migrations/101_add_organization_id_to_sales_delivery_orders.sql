-- Migration 101: Add organization_id to sales_delivery_orders

ALTER TABLE sales_delivery_orders ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
CREATE INDEX IF NOT EXISTS idx_sdo_organization ON sales_delivery_orders(organization_id);

-- Backfill from sales order's organization_id
UPDATE sales_delivery_orders sdo SET organization_id = (
    SELECT so.organization_id FROM sales_orders so WHERE so.id = sdo.sales_order_id
) WHERE sdo.organization_id IS NULL AND sdo.sales_order_id IS NOT NULL;

-- Fallback to tenant's first organization
UPDATE sales_delivery_orders sdo SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = sdo.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE sdo.organization_id IS NULL;
