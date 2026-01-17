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
    response_deadline DATE NOT NULL,
    requirements TEXT,
    evaluation_criteria TEXT,
    estimated_budget DECIMAL(20, 4) DEFAULT 0,
    currency VARCHAR(10) DEFAULT 'UZS',
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, rfq_number)
);

CREATE INDEX idx_rfqs_tenant ON rfqs(tenant_id);
CREATE INDEX idx_rfqs_status ON rfqs(tenant_id, status);
CREATE INDEX idx_rfqs_dates ON rfqs(issue_date, response_deadline);
CREATE INDEX idx_rfqs_number ON rfqs(rfq_number);

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
    required_delivery_date DATE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(rfq_id, line_number)
);

CREATE INDEX idx_rfq_items_tenant ON rfq_items(tenant_id);
CREATE INDEX idx_rfq_items_rfq ON rfq_items(rfq_id);
CREATE INDEX idx_rfq_items_product ON rfq_items(product_id);

-- ============================================
-- RFQ Vendors (invited vendors)
-- ============================================
CREATE TABLE IF NOT EXISTS rfq_vendors (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    rfq_id UUID NOT NULL REFERENCES rfqs(id) ON DELETE CASCADE,
    vendor_id UUID REFERENCES contacts(id) ON DELETE CASCADE,
    vendor_name VARCHAR(255) NOT NULL,
    vendor_email VARCHAR(255),
    invitation_sent_at TIMESTAMP WITH TIME ZONE,
    response_received_at TIMESTAMP WITH TIME ZONE,
    status VARCHAR(20) DEFAULT 'invited', -- invited, viewed, quoted, declined, no_response
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(rfq_id, vendor_id)
);

CREATE INDEX idx_rfq_vendors_tenant ON rfq_vendors(tenant_id);
CREATE INDEX idx_rfq_vendors_rfq ON rfq_vendors(rfq_id);
CREATE INDEX idx_rfq_vendors_vendor ON rfq_vendors(vendor_id);
CREATE INDEX idx_rfq_vendors_status ON rfq_vendors(status);

-- ============================================
-- Vendor Quotations (responses to RFQ)
-- ============================================
CREATE TABLE IF NOT EXISTS vendor_quotations (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    rfq_id UUID NOT NULL REFERENCES rfqs(id) ON DELETE CASCADE,
    rfq_vendor_id UUID NOT NULL REFERENCES rfq_vendors(id) ON DELETE CASCADE,
    vendor_id UUID REFERENCES contacts(id) ON DELETE SET NULL,
    quotation_number VARCHAR(50),
    submitted_at TIMESTAMP WITH TIME ZONE,
    valid_until DATE,
    total_amount DECIMAL(20, 4) DEFAULT 0,
    currency VARCHAR(10) DEFAULT 'UZS',
    payment_terms VARCHAR(100),
    delivery_terms VARCHAR(100),
    notes TEXT,
    evaluation_score DECIMAL(5, 2), -- Score based on evaluation criteria
    is_selected BOOLEAN DEFAULT false,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, quotation_number)
);

CREATE INDEX idx_vendor_quotations_tenant ON vendor_quotations(tenant_id);
CREATE INDEX idx_vendor_quotations_rfq ON vendor_quotations(rfq_id);
CREATE INDEX idx_vendor_quotations_vendor ON vendor_quotations(vendor_id);
CREATE INDEX idx_vendor_quotations_selected ON vendor_quotations(is_selected);

-- ============================================
-- Quotation Line Items
-- ============================================
CREATE TABLE IF NOT EXISTS quotation_items (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    quotation_id UUID NOT NULL REFERENCES vendor_quotations(id) ON DELETE CASCADE,
    rfq_item_id UUID REFERENCES rfq_items(id) ON DELETE SET NULL,
    line_number INTEGER NOT NULL,
    description TEXT NOT NULL,
    quantity DECIMAL(20, 4) NOT NULL,
    unit VARCHAR(50),
    unit_price DECIMAL(20, 4) NOT NULL,
    discount_percent DECIMAL(5, 2) DEFAULT 0,
    tax_percent DECIMAL(5, 2) DEFAULT 0,
    line_total DECIMAL(20, 4) GENERATED ALWAYS AS (
        quantity * unit_price * (1 - discount_percent / 100) * (1 + tax_percent / 100)
    ) STORED,
    delivery_time_days INTEGER,
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(quotation_id, line_number)
);

CREATE INDEX idx_quotation_items_tenant ON quotation_items(tenant_id);
CREATE INDEX idx_quotation_items_quotation ON quotation_items(quotation_id);
CREATE INDEX idx_quotation_items_rfq_item ON quotation_items(rfq_item_id);

-- ============================================
-- COMMENTS
-- ============================================
COMMENT ON TABLE rfqs IS 'Request for Quotation (RFQ) documents';
COMMENT ON TABLE rfq_items IS 'Line items for each RFQ';
COMMENT ON TABLE rfq_vendors IS 'Vendors invited to submit quotes for an RFQ';
COMMENT ON TABLE vendor_quotations IS 'Vendor responses/quotes to RFQs';
COMMENT ON TABLE quotation_items IS 'Line items in vendor quotations';

COMMENT ON COLUMN rfqs.status IS 'RFQ status: draft, sent, received, evaluating, awarded, cancelled';
COMMENT ON COLUMN rfq_vendors.status IS 'Vendor response status: invited, viewed, quoted, declined, no_response';
COMMENT ON COLUMN vendor_quotations.evaluation_score IS 'Score based on evaluation criteria (price, quality, delivery, etc.)';
COMMENT ON COLUMN vendor_quotations.is_selected IS 'Whether this quotation was selected/awarded';
