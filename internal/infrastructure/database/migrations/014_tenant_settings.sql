-- Migration: 013_tenant_settings
-- Description: Add tenant_settings table for storing admin settings per tenant

-- =====================================================
-- TENANT SETTINGS TABLE
-- =====================================================

CREATE TABLE IF NOT EXISTS tenant_settings (
    tenant_id UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    settings JSONB NOT NULL DEFAULT '{}',
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Index for quick lookup
CREATE INDEX IF NOT EXISTS idx_tenant_settings_updated ON tenant_settings(updated_at);

-- Comment
COMMENT ON TABLE tenant_settings IS 'Stores admin configuration settings per tenant';
COMMENT ON COLUMN tenant_settings.settings IS 'JSON object containing all admin settings (general, crm, sales, inventory, etc.)';
