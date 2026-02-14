-- Migration 094: Add organization_id to fixed_assets, asset_categories, depreciation_entries

-- fixed_assets
ALTER TABLE fixed_assets ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
CREATE INDEX IF NOT EXISTS idx_fixed_assets_organization ON fixed_assets(organization_id);

-- asset_categories
ALTER TABLE asset_categories ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
CREATE INDEX IF NOT EXISTS idx_asset_categories_organization ON asset_categories(organization_id);

-- depreciation_entries
ALTER TABLE depreciation_entries ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
CREATE INDEX IF NOT EXISTS idx_depreciation_entries_organization ON depreciation_entries(organization_id);

-- Backfill fixed_assets from tenant's first organization
UPDATE fixed_assets fa SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = fa.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE fa.organization_id IS NULL;

-- Backfill asset_categories from tenant's first organization
UPDATE asset_categories ac SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = ac.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE ac.organization_id IS NULL;

-- Backfill depreciation_entries from their parent fixed_asset's organization_id
UPDATE depreciation_entries de SET organization_id = (
    SELECT fa.organization_id FROM fixed_assets fa WHERE fa.id = de.asset_id
) WHERE de.organization_id IS NULL AND de.asset_id IS NOT NULL;

-- Fallback for any remaining depreciation_entries
UPDATE depreciation_entries de SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = de.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE de.organization_id IS NULL;
