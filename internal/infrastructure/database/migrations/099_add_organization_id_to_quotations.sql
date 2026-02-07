-- Migration 099: Add organization_id to quotations tables

-- sales_quotations
ALTER TABLE sales_quotations ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
CREATE INDEX IF NOT EXISTS idx_sales_quotations_organization ON sales_quotations(organization_id);

-- Backfill from customer's organization
UPDATE sales_quotations sq SET organization_id = (
    SELECT c.organization_id FROM contacts c WHERE c.id = sq.customer_id
) WHERE sq.organization_id IS NULL AND sq.customer_id IS NOT NULL;

-- Fallback to tenant's first organization
UPDATE sales_quotations sq SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = sq.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE sq.organization_id IS NULL;

-- quotations (CRM quotations)
ALTER TABLE quotations ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
CREATE INDEX IF NOT EXISTS idx_quotations_organization ON quotations(organization_id);

-- Backfill from contact's organization
UPDATE quotations q SET organization_id = (
    SELECT c.organization_id FROM contacts c WHERE c.id = q.contact_id
) WHERE q.organization_id IS NULL AND q.contact_id IS NOT NULL;

-- Fallback to tenant's first organization
UPDATE quotations q SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = q.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE q.organization_id IS NULL;
