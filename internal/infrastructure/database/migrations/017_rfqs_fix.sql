-- Fix RFQs module - add missing columns and tables

-- Add missing columns to rfqs table
ALTER TABLE rfqs ADD COLUMN IF NOT EXISTS deadline DATE;
ALTER TABLE rfqs ADD COLUMN IF NOT EXISTS terms TEXT;
ALTER TABLE rfqs ADD COLUMN IF NOT EXISTS notes TEXT;
ALTER TABLE rfqs ADD COLUMN IF NOT EXISTS winner_id UUID REFERENCES contacts(id) ON DELETE SET NULL;

-- Add index for winner_id
CREATE INDEX IF NOT EXISTS idx_rfqs_winner ON rfqs(winner_id);

-- Create rfq_invitations table (replaces rfq_vendors)
CREATE TABLE IF NOT EXISTS rfq_invitations (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    rfq_id UUID NOT NULL REFERENCES rfqs(id) ON DELETE CASCADE,
    vendor_id UUID NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    invited_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    responded_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(rfq_id, vendor_id)
);

CREATE INDEX IF NOT EXISTS idx_rfq_invitations_tenant ON rfq_invitations(tenant_id);
CREATE INDEX IF NOT EXISTS idx_rfq_invitations_rfq ON rfq_invitations(rfq_id);
CREATE INDEX IF NOT EXISTS idx_rfq_invitations_vendor ON rfq_invitations(vendor_id);

-- Create rfq_responses table
CREATE TABLE IF NOT EXISTS rfq_responses (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    rfq_id UUID NOT NULL REFERENCES rfqs(id) ON DELETE CASCADE,
    vendor_id UUID NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    total_amount DECIMAL(20, 4) NOT NULL,
    lead_time_days INTEGER,
    valid_until DATE,
    notes TEXT,
    attachments JSONB DEFAULT '[]',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(rfq_id, vendor_id)
);

CREATE INDEX IF NOT EXISTS idx_rfq_responses_tenant ON rfq_responses(tenant_id);
CREATE INDEX IF NOT EXISTS idx_rfq_responses_rfq ON rfq_responses(rfq_id);
CREATE INDEX IF NOT EXISTS idx_rfq_responses_vendor ON rfq_responses(vendor_id);

-- Create rfq_response_items table
CREATE TABLE IF NOT EXISTS rfq_response_items (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    response_id UUID NOT NULL REFERENCES rfq_responses(id) ON DELETE CASCADE,
    rfq_item_id UUID NOT NULL REFERENCES rfq_items(id) ON DELETE CASCADE,
    unit_price DECIMAL(20, 4) NOT NULL,
    quantity DECIMAL(20, 4) NOT NULL,
    lead_time_days INTEGER,
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(response_id, rfq_item_id)
);

CREATE INDEX IF NOT EXISTS idx_rfq_response_items_tenant ON rfq_response_items(tenant_id);
CREATE INDEX IF NOT EXISTS idx_rfq_response_items_response ON rfq_response_items(response_id);
CREATE INDEX IF NOT EXISTS idx_rfq_response_items_rfq_item ON rfq_response_items(rfq_item_id);

-- Comments
COMMENT ON TABLE rfq_invitations IS 'Vendors invited to submit quotations';
COMMENT ON TABLE rfq_responses IS 'Vendor responses/quotations to RFQs';
COMMENT ON TABLE rfq_response_items IS 'Line items in vendor quotations';
COMMENT ON COLUMN rfqs.winner_id IS 'Vendor awarded the contract';
