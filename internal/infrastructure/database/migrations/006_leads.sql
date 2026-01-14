-- Migration: 006_leads
-- Description: Create leads table for CRM lead management

-- Leads table
CREATE TABLE IF NOT EXISTS leads (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    contact_name VARCHAR(255) NOT NULL,
    company_name VARCHAR(255),
    email VARCHAR(255) NOT NULL,
    phone VARCHAR(50),
    status VARCHAR(20) NOT NULL DEFAULT 'new',
    source VARCHAR(30) NOT NULL DEFAULT 'website',
    notes TEXT,
    expected_value DECIMAL(15, 2),
    assigned_to UUID REFERENCES users(id) ON DELETE SET NULL,
    converted_to UUID REFERENCES contacts(id) ON DELETE SET NULL,
    converted_at TIMESTAMP,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP,

    CONSTRAINT chk_lead_status CHECK (status IN ('new', 'contacted', 'in_progress', 'qualified', 'lost')),
    CONSTRAINT chk_lead_source CHECK (source IN ('website', 'referral', 'social_media', 'cold_call', 'advertisement', 'other'))
);

-- Indexes for efficient querying
CREATE INDEX IF NOT EXISTS idx_leads_tenant ON leads(tenant_id);
CREATE INDEX IF NOT EXISTS idx_leads_status ON leads(tenant_id, status);
CREATE INDEX IF NOT EXISTS idx_leads_source ON leads(tenant_id, source);
CREATE INDEX IF NOT EXISTS idx_leads_assigned ON leads(tenant_id, assigned_to);
CREATE INDEX IF NOT EXISTS idx_leads_email ON leads(tenant_id, email);
CREATE INDEX IF NOT EXISTS idx_leads_created ON leads(tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_leads_deleted ON leads(tenant_id, deleted_at) WHERE deleted_at IS NULL;

-- Trigger to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_leads_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trigger_leads_updated_at ON leads;
CREATE TRIGGER trigger_leads_updated_at
    BEFORE UPDATE ON leads
    FOR EACH ROW
    EXECUTE FUNCTION update_leads_updated_at();

-- Comments
COMMENT ON TABLE leads IS 'Stores sales leads for CRM';
COMMENT ON COLUMN leads.status IS 'Lead status: new, contacted, in_progress, qualified, lost';
COMMENT ON COLUMN leads.source IS 'Lead source: website, referral, social_media, cold_call, advertisement, other';
COMMENT ON COLUMN leads.expected_value IS 'Expected deal value if lead converts';
COMMENT ON COLUMN leads.converted_to IS 'Reference to contact if lead was converted';
COMMENT ON COLUMN leads.converted_at IS 'Timestamp when lead was converted to contact';
