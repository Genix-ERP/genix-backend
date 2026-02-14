-- Migration 091: Backfill organization_id for payments
-- Payments had the column but CreatePayment never set it

-- Backfill from contact's organization_id
UPDATE payments p SET organization_id = (
    SELECT c.organization_id FROM contacts c WHERE c.id = p.contact_id
) WHERE p.organization_id IS NULL AND p.contact_id IS NOT NULL;

-- Fallback: use tenant's first organization for any remaining
UPDATE payments p SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = p.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE p.organization_id IS NULL;
