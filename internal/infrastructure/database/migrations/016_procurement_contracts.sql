-- GenixERP Procurement Contracts Module Schema Migration

-- ============================================
-- PROCUREMENT CONTRACTS (Vendor Contracts)
-- ============================================
CREATE TABLE IF NOT EXISTS procurement_contracts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    contract_number VARCHAR(50) NOT NULL,
    vendor_id UUID REFERENCES contacts(id) ON DELETE SET NULL,
    vendor_name VARCHAR(255) NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    contract_type VARCHAR(50) NOT NULL, -- annual, fixed, project, framework, blanket
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    value DECIMAL(20, 4) DEFAULT 0,
    currency VARCHAR(10) DEFAULT 'UZS',
    payment_terms VARCHAR(100),
    delivery_terms VARCHAR(100),
    terms TEXT,
    auto_renew BOOLEAN DEFAULT false,
    renewal_notice_days INTEGER DEFAULT 30,
    status VARCHAR(20) DEFAULT 'draft', -- draft, active, expired, terminated, renewed
    signed_by_vendor BOOLEAN DEFAULT false,
    signed_by_company BOOLEAN DEFAULT false,
    vendor_signature_date DATE,
    company_signature_date DATE,
    attachments JSONB DEFAULT '[]',
    performance_rating DECIMAL(3, 2), -- Rating out of 5.00
    notes TEXT,
    created_by UUID REFERENCES users(id),
    approved_by UUID REFERENCES users(id),
    approved_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, contract_number)
);

CREATE INDEX idx_procurement_contracts_tenant ON procurement_contracts(tenant_id);
CREATE INDEX idx_procurement_contracts_supplier ON procurement_contracts(vendor_id);
CREATE INDEX idx_procurement_contracts_status ON procurement_contracts(tenant_id, status);
CREATE INDEX idx_procurement_contracts_dates ON procurement_contracts(start_date, end_date);
CREATE INDEX idx_procurement_contracts_type ON procurement_contracts(contract_type);
CREATE INDEX idx_procurement_contracts_number ON procurement_contracts(contract_number);

-- ============================================
-- CONTRACT ITEMS (for framework/blanket contracts)
-- ============================================
CREATE TABLE IF NOT EXISTS procurement_contract_items (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    contract_id UUID NOT NULL REFERENCES procurement_contracts(id) ON DELETE CASCADE,
    line_number INTEGER NOT NULL,
    product_id UUID REFERENCES products(id) ON DELETE SET NULL,
    description TEXT NOT NULL,
    quantity DECIMAL(20, 4), -- For fixed quantity contracts
    unit VARCHAR(50),
    unit_price DECIMAL(20, 4) NOT NULL,
    discount_percent DECIMAL(5, 2) DEFAULT 0,
    max_quantity DECIMAL(20, 4), -- For blanket contracts
    min_order_quantity DECIMAL(20, 4), -- Minimum order quantity per release
    lead_time_days INTEGER,
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(contract_id, line_number)
);

CREATE INDEX idx_procurement_contract_items_tenant ON procurement_contract_items(tenant_id);
CREATE INDEX idx_procurement_contract_items_contract ON procurement_contract_items(contract_id);
CREATE INDEX idx_procurement_contract_items_product ON procurement_contract_items(product_id);

-- ============================================
-- CONTRACT MILESTONES (for project-based contracts)
-- ============================================
CREATE TABLE IF NOT EXISTS contract_milestones (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    contract_id UUID NOT NULL REFERENCES procurement_contracts(id) ON DELETE CASCADE,
    milestone_number INTEGER NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    due_date DATE NOT NULL,
    amount DECIMAL(20, 4),
    status VARCHAR(20) DEFAULT 'pending', -- pending, in_progress, completed, delayed, cancelled
    completion_date DATE,
    completion_notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(contract_id, milestone_number)
);

CREATE INDEX idx_contract_milestones_tenant ON contract_milestones(tenant_id);
CREATE INDEX idx_contract_milestones_contract ON contract_milestones(contract_id);
CREATE INDEX idx_contract_milestones_status ON contract_milestones(status);
CREATE INDEX idx_contract_milestones_due_date ON contract_milestones(due_date);

-- ============================================
-- CONTRACT AMENDMENTS/CHANGES
-- ============================================
CREATE TABLE IF NOT EXISTS contract_amendments (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    contract_id UUID NOT NULL REFERENCES procurement_contracts(id) ON DELETE CASCADE,
    amendment_number INTEGER NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT NOT NULL,
    effective_date DATE NOT NULL,
    old_value JSONB, -- Store changed fields as JSON
    new_value JSONB,
    reason TEXT,
    status VARCHAR(20) DEFAULT 'draft', -- draft, approved, rejected
    approved_by UUID REFERENCES users(id),
    approved_at TIMESTAMP WITH TIME ZONE,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(contract_id, amendment_number)
);

CREATE INDEX idx_contract_amendments_tenant ON contract_amendments(tenant_id);
CREATE INDEX idx_contract_amendments_contract ON contract_amendments(contract_id);
CREATE INDEX idx_contract_amendments_status ON contract_amendments(status);

-- ============================================
-- COMMENTS
-- ============================================
COMMENT ON TABLE procurement_contracts IS 'Vendor procurement contracts';
COMMENT ON TABLE procurement_contract_items IS 'Line items for framework/blanket contracts';
COMMENT ON TABLE contract_milestones IS 'Milestones for project-based contracts';
COMMENT ON TABLE contract_amendments IS 'Contract amendments and change orders';

COMMENT ON COLUMN procurement_contracts.contract_type IS 'Contract type: annual, fixed, project, framework, blanket';
COMMENT ON COLUMN procurement_contracts.status IS 'Contract status: draft, active, expired, terminated, renewed';
COMMENT ON COLUMN procurement_contracts.auto_renew IS 'Whether contract auto-renews at expiration';
COMMENT ON COLUMN procurement_contracts.performance_rating IS 'Supplier performance rating (0-5)';
COMMENT ON COLUMN procurement_contract_items.max_quantity IS 'Maximum quantity allowed (for blanket contracts)';
COMMENT ON COLUMN procurement_contract_items.min_order_quantity IS 'Minimum order quantity per release';
COMMENT ON COLUMN contract_milestones.status IS 'Milestone status: pending, in_progress, completed, delayed, cancelled';
