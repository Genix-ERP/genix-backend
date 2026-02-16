-- GenixERP RFQ (Request for Quotation) Module Schema Migration

-- ============================================
-- RFQs (Request for Quotation)
-- ============================================
CREATE TABLE IF NOT EXISTS rfqs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    rfq_number VARCHAR(50) NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    status VARCHAR(20) DEFAULT 'draft', -- draft, sent, received, evaluating, awarded, cancelled
    issue_date DATE NOT NULL,
    deadline DATE NOT NULL,
    terms TEXT,
    notes TEXT,
    winner_id UUID REFERENCES contacts(id) ON DELETE SET NULL,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, rfq_number)
);

CREATE INDEX idx_rfqs_tenant ON rfqs(tenant_id);
CREATE INDEX idx_rfqs_status ON rfqs(tenant_id, status);
CREATE INDEX idx_rfqs_dates ON rfqs(issue_date, deadline);
CREATE INDEX idx_rfqs_number ON rfqs(rfq_number);
CREATE INDEX idx_rfqs_winner ON rfqs(winner_id);

-- ============================================
-- RFQ Items
-- ============================================
CREATE TABLE IF NOT EXISTS rfq_items (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    rfq_id UUID NOT NULL REFERENCES rfqs(id) ON DELETE CASCADE,
    line_number INTEGER NOT NULL,
    product_id UUID REFERENCES products(id) ON DELETE SET NULL,
    description TEXT NOT NULL,
    quantity DECIMAL(20, 4) NOT NULL,
    unit VARCHAR(50),
    specifications TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(rfq_id, line_number)
);

CREATE INDEX idx_rfq_items_tenant ON rfq_items(tenant_id);
CREATE INDEX idx_rfq_items_rfq ON rfq_items(rfq_id);
CREATE INDEX idx_rfq_items_product ON rfq_items(product_id);

-- ============================================
-- RFQ Invitations (vendors invited to quote)
-- ============================================
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

CREATE INDEX idx_rfq_invitations_tenant ON rfq_invitations(tenant_id);
CREATE INDEX idx_rfq_invitations_rfq ON rfq_invitations(rfq_id);
CREATE INDEX idx_rfq_invitations_vendor ON rfq_invitations(vendor_id);

-- ============================================
-- RFQ Responses (vendor quotations)
-- ============================================
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

CREATE INDEX idx_rfq_responses_tenant ON rfq_responses(tenant_id);
CREATE INDEX idx_rfq_responses_rfq ON rfq_responses(rfq_id);
CREATE INDEX idx_rfq_responses_vendor ON rfq_responses(vendor_id);

-- ============================================
-- RFQ Response Items
-- ============================================
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

CREATE INDEX idx_rfq_response_items_tenant ON rfq_response_items(tenant_id);
CREATE INDEX idx_rfq_response_items_response ON rfq_response_items(response_id);
CREATE INDEX idx_rfq_response_items_rfq_item ON rfq_response_items(rfq_item_id);

-- ============================================
-- COMMENTS
-- ============================================
COMMENT ON TABLE rfqs IS 'Request for Quotation documents';
COMMENT ON TABLE rfq_items IS 'Line items in RFQ';
COMMENT ON TABLE rfq_invitations IS 'Vendors invited to submit quotations';
COMMENT ON TABLE rfq_responses IS 'Vendor responses/quotations to RFQs';
COMMENT ON TABLE rfq_response_items IS 'Line items in vendor quotations';

COMMENT ON COLUMN rfqs.status IS 'RFQ status: draft, sent, received, evaluating, awarded, cancelled';
COMMENT ON COLUMN rfqs.winner_id IS 'Vendor awarded the contract';
