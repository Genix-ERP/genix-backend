-- Migration 095: Fix fixed_assets unique constraint to be org-scoped

ALTER TABLE fixed_assets DROP CONSTRAINT IF EXISTS fixed_assets_tenant_id_asset_code_key;
ALTER TABLE fixed_assets ADD CONSTRAINT fixed_assets_tenant_org_asset_code_key UNIQUE (tenant_id, organization_id, asset_code);
