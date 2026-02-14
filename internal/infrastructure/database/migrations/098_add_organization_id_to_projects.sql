-- Migration 098: Add organization_id to projects

ALTER TABLE projects ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
CREATE INDEX IF NOT EXISTS idx_projects_organization ON projects(organization_id);

-- Backfill from client's organization_id
UPDATE projects p SET organization_id = (
    SELECT c.organization_id FROM contacts c WHERE c.id = p.client_id
) WHERE p.organization_id IS NULL AND p.client_id IS NOT NULL;

-- Backfill fallback to tenant's first organization
UPDATE projects p SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = p.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE p.organization_id IS NULL;
