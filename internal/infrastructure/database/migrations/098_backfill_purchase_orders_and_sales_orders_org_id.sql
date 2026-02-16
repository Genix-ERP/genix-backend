-- Migration 096: Backfill organization_id for purchase_orders and sales_orders

-- purchase_orders: backfill from vendor's organization_id
UPDATE purchase_orders po SET organization_id = (
    SELECT c.organization_id FROM contacts c WHERE c.id = po.vendor_id
) WHERE po.organization_id IS NULL AND po.vendor_id IS NOT NULL;

-- purchase_orders: fallback to tenant's first organization
UPDATE purchase_orders po SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = po.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE po.organization_id IS NULL;

-- sales_orders: backfill from customer's organization_id
UPDATE sales_orders so SET organization_id = (
    SELECT c.organization_id FROM contacts c WHERE c.id = so.customer_id
) WHERE so.organization_id IS NULL AND so.customer_id IS NOT NULL;

-- sales_orders: fallback to tenant's first organization
UPDATE sales_orders so SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = so.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE so.organization_id IS NULL;
