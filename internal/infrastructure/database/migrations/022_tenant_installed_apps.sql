-- Migration: tenant_installed_apps
-- Description: Store installed apps per tenant for multi-device sync

-- Create tenant_installed_apps table
CREATE TABLE IF NOT EXISTS tenant_installed_apps (
    id SERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    app_id VARCHAR(100) NOT NULL,
    app_name VARCHAR(255) NOT NULL,
    app_description TEXT,
    app_version VARCHAR(50) DEFAULT '1.0',
    app_icon VARCHAR(255),
    app_color VARCHAR(50),
    status VARCHAR(50) DEFAULT 'active', -- active, inactive, suspended
    installed_by UUID REFERENCES users(id),
    installed_date TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_date TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    settings JSONB DEFAULT '{}',
    UNIQUE(tenant_id, app_id)
);

-- Create index for faster lookups
CREATE INDEX IF NOT EXISTS idx_tenant_installed_apps_tenant ON tenant_installed_apps(tenant_id);
CREATE INDEX IF NOT EXISTS idx_tenant_installed_apps_status ON tenant_installed_apps(status);

-- Add comment
COMMENT ON TABLE tenant_installed_apps IS 'Stores installed applications per tenant for multi-device synchronization';
