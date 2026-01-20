-- Migration: Cargo Module Tables
-- Description: Creates tables for international freight forwarding and distribution system

-- Shipments table
CREATE TABLE IF NOT EXISTS cargo_shipments (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    tracking_number VARCHAR(255) NOT NULL,
    supplier_country VARCHAR(100) NOT NULL,
    supplier_company VARCHAR(255),

    -- Dates
    expected_date TIMESTAMP,
    actual_arrival_date TIMESTAMP,

    -- Status tracking
    status VARCHAR(50) NOT NULL DEFAULT 'ordered' CHECK (status IN ('ordered', 'in_transit', 'in_customs', 'received', 'distributed')),

    -- Costs
    transport_cost DECIMAL(15, 2) DEFAULT 0,
    customs_cost DECIMAL(15, 2) DEFAULT 0,
    insurance_cost DECIMAL(15, 2) DEFAULT 0,
    other_cost DECIMAL(15, 2) DEFAULT 0,
    total_cost DECIMAL(15, 2) GENERATED ALWAYS AS (transport_cost + customs_cost + insurance_cost + other_cost) STORED,

    -- Metadata
    notes TEXT,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_date TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_date TIMESTAMP NOT NULL DEFAULT NOW(),

    CONSTRAINT unique_tracking_per_tenant UNIQUE (tenant_id, tracking_number)
);

CREATE INDEX idx_cargo_shipments_tenant ON cargo_shipments(tenant_id);
CREATE INDEX idx_cargo_shipments_status ON cargo_shipments(status);
CREATE INDEX idx_cargo_shipments_tracking ON cargo_shipments(tracking_number);
CREATE INDEX idx_cargo_shipments_expected_date ON cargo_shipments(expected_date);

-- Shipment items table
CREATE TABLE IF NOT EXISTS cargo_shipment_items (
    id BIGSERIAL PRIMARY KEY,
    shipment_id BIGINT NOT NULL REFERENCES cargo_shipments(id) ON DELETE CASCADE,

    -- Item details
    item_name VARCHAR(255) NOT NULL,
    quantity DECIMAL(15, 3) NOT NULL,
    unit_price DECIMAL(15, 2) NOT NULL,
    currency VARCHAR(3) NOT NULL DEFAULT 'USD' CHECK (currency IN ('USD', 'UZS', 'EUR')),
    total_price DECIMAL(15, 2) GENERATED ALWAYS AS (quantity * unit_price) STORED,

    -- Additional details
    hs_code VARCHAR(50), -- Harmonized System code for customs
    description TEXT,

    created_date TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_date TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_cargo_items_shipment ON cargo_shipment_items(shipment_id);

-- Shipment status history table
CREATE TABLE IF NOT EXISTS cargo_shipment_status_history (
    id BIGSERIAL PRIMARY KEY,
    shipment_id BIGINT NOT NULL REFERENCES cargo_shipments(id) ON DELETE CASCADE,
    status VARCHAR(50) NOT NULL,
    note TEXT,
    location VARCHAR(255),
    changed_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    changed_date TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_cargo_status_history_shipment ON cargo_shipment_status_history(shipment_id);
CREATE INDEX idx_cargo_status_history_date ON cargo_shipment_status_history(changed_date);

-- Distribution records table (B2B/B2C distribution)
CREATE TABLE IF NOT EXISTS cargo_distributions (
    id BIGSERIAL PRIMARY KEY,
    shipment_id BIGINT NOT NULL REFERENCES cargo_shipments(id) ON DELETE CASCADE,

    -- Recipient company
    recipient_tenant_id UUID REFERENCES tenants(id) ON DELETE SET NULL,
    recipient_company_name VARCHAR(255) NOT NULL,
    recipient_company_type VARCHAR(10) NOT NULL CHECK (recipient_company_type IN ('B2B', 'B2C')),

    -- Distribution details
    distribution_date TIMESTAMP NOT NULL DEFAULT NOW(),
    total_items_cost DECIMAL(15, 2) NOT NULL,
    allocated_costs DECIMAL(15, 2) NOT NULL, -- Proportional share of shipment costs
    total_cost DECIMAL(15, 2) GENERATED ALWAYS AS (total_items_cost + allocated_costs) STORED,

    -- Document references
    invoice_number VARCHAR(100),
    waybill_number VARCHAR(100),

    notes TEXT,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_date TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_cargo_distributions_shipment ON cargo_distributions(shipment_id);
CREATE INDEX idx_cargo_distributions_recipient ON cargo_distributions(recipient_tenant_id);
CREATE INDEX idx_cargo_distributions_date ON cargo_distributions(distribution_date);

-- Distribution items table
CREATE TABLE IF NOT EXISTS cargo_distribution_items (
    id BIGSERIAL PRIMARY KEY,
    distribution_id BIGINT NOT NULL REFERENCES cargo_distributions(id) ON DELETE CASCADE,
    shipment_item_id BIGINT NOT NULL REFERENCES cargo_shipment_items(id) ON DELETE CASCADE,

    quantity DECIMAL(15, 3) NOT NULL,
    unit_cost DECIMAL(15, 2) NOT NULL,
    total_cost DECIMAL(15, 2) GENERATED ALWAYS AS (quantity * unit_cost) STORED,

    created_date TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_cargo_dist_items_distribution ON cargo_distribution_items(distribution_id);
CREATE INDEX idx_cargo_dist_items_shipment_item ON cargo_distribution_items(shipment_item_id);

-- Cargo cash register table
CREATE TABLE IF NOT EXISTS cargo_cash_transactions (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Transaction details
    transaction_type VARCHAR(20) NOT NULL CHECK (transaction_type IN ('income', 'expense')),
    amount DECIMAL(15, 2) NOT NULL,
    currency VARCHAR(3) NOT NULL DEFAULT 'UZS' CHECK (currency IN ('USD', 'UZS', 'EUR')),

    -- Categories
    category VARCHAR(100) NOT NULL CHECK (category IN (
        'transport', 'customs', 'insurance', 'other_expense',
        'payment_from_b2b', 'payment_from_b2c', 'transfer_to_b2b', 'other_income'
    )),

    -- References
    shipment_id BIGINT REFERENCES cargo_shipments(id) ON DELETE SET NULL,
    distribution_id BIGINT REFERENCES cargo_distributions(id) ON DELETE SET NULL,
    related_tenant_id UUID REFERENCES tenants(id) ON DELETE SET NULL,

    -- Details
    description TEXT,
    reference_number VARCHAR(100),

    -- Metadata
    transaction_date TIMESTAMP NOT NULL DEFAULT NOW(),
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_date TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_cargo_cash_tenant ON cargo_cash_transactions(tenant_id);
CREATE INDEX idx_cargo_cash_type ON cargo_cash_transactions(transaction_type);
CREATE INDEX idx_cargo_cash_category ON cargo_cash_transactions(category);
CREATE INDEX idx_cargo_cash_date ON cargo_cash_transactions(transaction_date);
CREATE INDEX idx_cargo_cash_shipment ON cargo_cash_transactions(shipment_id);

-- Company accounts table (B2B/B2C accounts receivable/payable)
CREATE TABLE IF NOT EXISTS cargo_company_accounts (
    id BIGSERIAL PRIMARY KEY,
    cargo_tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    related_tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    company_type VARCHAR(10) NOT NULL CHECK (company_type IN ('B2B', 'B2C')),

    -- Balances
    total_debt DECIMAL(15, 2) DEFAULT 0,
    total_credit DECIMAL(15, 2) DEFAULT 0,
    balance DECIMAL(15, 2) GENERATED ALWAYS AS (total_credit - total_debt) STORED,

    -- Currency
    currency VARCHAR(3) NOT NULL DEFAULT 'UZS',

    -- Metadata
    last_transaction_date TIMESTAMP,
    created_date TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_date TIMESTAMP NOT NULL DEFAULT NOW(),

    CONSTRAINT unique_cargo_tenant_account UNIQUE (cargo_tenant_id, related_tenant_id)
);

CREATE INDEX idx_cargo_accounts_cargo_tenant ON cargo_company_accounts(cargo_tenant_id);
CREATE INDEX idx_cargo_accounts_related_tenant ON cargo_company_accounts(related_tenant_id);

-- Triggers for updated_date
CREATE OR REPLACE FUNCTION update_cargo_updated_date()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_date = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_cargo_shipments_updated
    BEFORE UPDATE ON cargo_shipments
    FOR EACH ROW
    EXECUTE FUNCTION update_cargo_updated_date();

CREATE TRIGGER trigger_cargo_items_updated
    BEFORE UPDATE ON cargo_shipment_items
    FOR EACH ROW
    EXECUTE FUNCTION update_cargo_updated_date();

CREATE TRIGGER trigger_cargo_accounts_updated
    BEFORE UPDATE ON cargo_company_accounts
    FOR EACH ROW
    EXECUTE FUNCTION update_cargo_updated_date();

-- Comments
COMMENT ON TABLE cargo_shipments IS 'Main shipments table for cargo module';
COMMENT ON TABLE cargo_shipment_items IS 'Items in each shipment';
COMMENT ON TABLE cargo_shipment_status_history IS 'Tracking history of shipment status changes';
COMMENT ON TABLE cargo_distributions IS 'Distribution of goods to B2B/B2C companies';
COMMENT ON TABLE cargo_distribution_items IS 'Individual items distributed';
COMMENT ON TABLE cargo_cash_transactions IS 'Cargo cash register transactions';
COMMENT ON TABLE cargo_company_accounts IS 'Accounts receivable/payable with B2B/B2C companies';
