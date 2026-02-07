-- Migration 092: Backfill organization_id for accounts
-- Accounts had the column but some rows were created without it

UPDATE accounts a SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = a.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE a.organization_id IS NULL;
