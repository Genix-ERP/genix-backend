-- Migration 100: Add organization_id to discounts

ALTER TABLE discounts ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
CREATE INDEX IF NOT EXISTS idx_discounts_organization ON discounts(organization_id);

-- Backfill to tenant's first organization
UPDATE discounts d SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = d.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE d.organization_id IS NULL;
