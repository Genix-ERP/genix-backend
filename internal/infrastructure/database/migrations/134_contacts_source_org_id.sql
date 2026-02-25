-- Migration: 134_contacts_source_org_id.sql
-- Description: Add source_organization_id to contacts for tracking intercompany vendor/customer links
-- Date: 2026-02-25

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'contacts' AND column_name = 'source_organization_id'
    ) THEN
        ALTER TABLE contacts ADD COLUMN source_organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL;
        CREATE INDEX idx_contacts_source_org ON contacts(tenant_id, organization_id, source_organization_id) WHERE source_organization_id IS NOT NULL;
    END IF;
END
$$;
