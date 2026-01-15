-- Migration: 004_procurement_extensions
-- Description: Add RFQ and Contracts tables for procurement module

-- =====================================================
-- REQUEST FOR QUOTATION (RFQ) TABLES
-- =====================================================

-- Main RFQ table
CREATE TABLE IF NOT EXISTS rfqs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    rfq_number VARCHAR(50) NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    status VARCHAR(20) NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'open', 'closed', 'cancelled')),
    deadline TIMESTAMP WITH TIME ZONE,
    terms TEXT,
    notes TEXT,
    winner_id UUID REFERENCES contacts(id),
    custom_fields JSONB DEFAULT '{}',
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, rfq_number)
);

-- RFQ Items
CREATE TABLE IF NOT EXISTS rfq_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rfq_id UUID NOT NULL REFERENCES rfqs(id) ON DELETE CASCADE,
    product_id UUID REFERENCES products(id),
    description VARCHAR(500) NOT NULL,
    quantity DECIMAL(15, 4) NOT NULL DEFAULT 1,
    unit_id UUID REFERENCES units_of_measure(id),
    specs TEXT,
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- RFQ Vendor Invitations
CREATE TABLE IF NOT EXISTS rfq_invitations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rfq_id UUID NOT NULL REFERENCES rfqs(id) ON DELETE CASCADE,
    vendor_id UUID NOT NULL REFERENCES contacts(id),
    invited_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    viewed_at TIMESTAMP WITH TIME ZONE,
    responded_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(rfq_id, vendor_id)
);

-- RFQ Responses from vendors
CREATE TABLE IF NOT EXISTS rfq_responses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rfq_id UUID NOT NULL REFERENCES rfqs(id) ON DELETE CASCADE,
    vendor_id UUID NOT NULL REFERENCES contacts(id),
    total_amount DECIMAL(15, 2) NOT NULL DEFAULT 0,
    lead_time_days INTEGER DEFAULT 0,
    valid_until TIMESTAMP WITH TIME ZONE,
    notes TEXT,
    is_winner BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(rfq_id, vendor_id)
);

-- RFQ Response Items
CREATE TABLE IF NOT EXISTS rfq_response_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    response_id UUID NOT NULL REFERENCES rfq_responses(id) ON DELETE CASCADE,
    item_id UUID NOT NULL REFERENCES rfq_items(id),
    unit_price DECIMAL(15, 4) NOT NULL DEFAULT 0,
    total_price DECIMAL(15, 2) NOT NULL DEFAULT 0,
    notes TEXT,
    UNIQUE(response_id, item_id)
);

-- =====================================================
-- PROCUREMENT CONTRACTS TABLE
-- =====================================================

CREATE TABLE IF NOT EXISTS procurement_contracts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    contract_number VARCHAR(50) NOT NULL,
    title VARCHAR(255) NOT NULL,
    vendor_id UUID NOT NULL REFERENCES contacts(id),
    contract_type VARCHAR(20) NOT NULL DEFAULT 'fixed' CHECK (contract_type IN ('fixed', 'annual', 'monthly', 'project')),
    status VARCHAR(20) NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'active', 'expired', 'terminated')),
    start_date DATE NOT NULL,
    end_date DATE,
    value DECIMAL(15, 2) NOT NULL DEFAULT 0,
    currency_id UUID REFERENCES currencies(id),
    terms TEXT,
    description TEXT,
    auto_renewal BOOLEAN DEFAULT FALSE,
    renewal_term_days INTEGER DEFAULT 0,
    notes TEXT,
    document_url VARCHAR(500),
    custom_fields JSONB DEFAULT '{}',
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, contract_number)
);

-- =====================================================
-- SUPPLIER PERFORMANCE TABLE
-- =====================================================

CREATE TABLE IF NOT EXISTS supplier_performance (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    vendor_id UUID NOT NULL REFERENCES contacts(id),
    period_start DATE NOT NULL,
    period_end DATE NOT NULL,
    total_orders INTEGER DEFAULT 0,
    on_time_deliveries INTEGER DEFAULT 0,
    late_deliveries INTEGER DEFAULT 0,
    quality_score DECIMAL(5, 2) DEFAULT 0,
    price_competitiveness DECIMAL(5, 2) DEFAULT 0,
    overall_rating DECIMAL(3, 2) DEFAULT 0,
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- =====================================================
-- PRICE HISTORY TABLE
-- =====================================================

CREATE TABLE IF NOT EXISTS supplier_price_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES products(id),
    vendor_id UUID NOT NULL REFERENCES contacts(id),
    unit_price DECIMAL(15, 4) NOT NULL,
    currency_id UUID REFERENCES currencies(id),
    effective_date DATE NOT NULL DEFAULT CURRENT_DATE,
    min_quantity DECIMAL(15, 4) DEFAULT 1,
    notes TEXT,
    source VARCHAR(50), -- 'purchase_order', 'rfq', 'manual'
    source_id UUID,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- =====================================================
-- INDEXES
-- =====================================================

CREATE INDEX IF NOT EXISTS idx_rfqs_tenant_status ON rfqs(tenant_id, status) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_rfqs_deadline ON rfqs(deadline) WHERE deleted_at IS NULL AND status = 'open';
CREATE INDEX IF NOT EXISTS idx_rfq_items_rfq ON rfq_items(rfq_id);
CREATE INDEX IF NOT EXISTS idx_rfq_invitations_vendor ON rfq_invitations(vendor_id);
CREATE INDEX IF NOT EXISTS idx_rfq_responses_rfq ON rfq_responses(rfq_id);

CREATE INDEX IF NOT EXISTS idx_contracts_tenant_status ON procurement_contracts(tenant_id, status) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_contracts_vendor ON procurement_contracts(vendor_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_contracts_end_date ON procurement_contracts(end_date) WHERE deleted_at IS NULL AND status = 'active';

CREATE INDEX IF NOT EXISTS idx_supplier_performance_vendor ON supplier_performance(vendor_id);
CREATE INDEX IF NOT EXISTS idx_supplier_price_history_product ON supplier_price_history(product_id, vendor_id);
CREATE INDEX IF NOT EXISTS idx_supplier_price_history_date ON supplier_price_history(effective_date);

-- =====================================================
-- TRIGGERS FOR UPDATED_AT
-- =====================================================

CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

DROP TRIGGER IF EXISTS update_rfqs_updated_at ON rfqs;
CREATE TRIGGER update_rfqs_updated_at BEFORE UPDATE ON rfqs FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_rfq_items_updated_at ON rfq_items;
CREATE TRIGGER update_rfq_items_updated_at BEFORE UPDATE ON rfq_items FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_rfq_responses_updated_at ON rfq_responses;
CREATE TRIGGER update_rfq_responses_updated_at BEFORE UPDATE ON rfq_responses FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_procurement_contracts_updated_at ON procurement_contracts;
CREATE TRIGGER update_procurement_contracts_updated_at BEFORE UPDATE ON procurement_contracts FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
