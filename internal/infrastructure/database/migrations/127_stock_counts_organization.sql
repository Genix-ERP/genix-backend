-- Migration: 127_stock_counts_organization.sql
-- Description: Add organization_id to stock_counts table
-- Date: 2026-02-19

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'stock_counts' AND column_name = 'organization_id'
    ) THEN
        ALTER TABLE stock_counts ADD COLUMN organization_id UUID REFERENCES organizations(id);
        CREATE INDEX IF NOT EXISTS idx_stock_counts_organization ON stock_counts(organization_id);
    END IF;
END
$$;
