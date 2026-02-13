-- Migration 119: Inter-company Transfers
-- Adds support for product and money transfers between companies within the same tenant

-- =====================================================
-- INTERCOMPANY TRANSFER ORDERS (Product Transfers)
-- =====================================================
CREATE TABLE IF NOT EXISTS intercompany_transfers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Transfer identification
    transfer_number VARCHAR(50) NOT NULL,
    transfer_type VARCHAR(20) NOT NULL DEFAULT 'product', -- 'product', 'money'

    -- Organizations involved
    from_organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    to_organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,

    -- Warehouses for product transfers
    from_warehouse_id UUID REFERENCES warehouses(id) ON DELETE SET NULL,
    to_warehouse_id UUID REFERENCES warehouses(id) ON DELETE SET NULL,

    -- Dates
    transfer_date DATE NOT NULL,
    expected_date DATE,
    received_date DATE,

    -- Status workflow: draft -> pending_approval -> approved -> in_transit -> received -> invoiced -> settled
    status VARCHAR(30) NOT NULL DEFAULT 'draft',

    -- Pricing for inventory transfers
    pricing_method VARCHAR(20) DEFAULT 'cost', -- 'cost', 'markup', 'list_price', 'negotiated'
    markup_percent DECIMAL(5, 2) DEFAULT 0,

    -- Currency
    currency_id UUID REFERENCES currencies(id),
    exchange_rate DECIMAL(18, 8) DEFAULT 1,

    -- Totals (calculated from lines)
    total_amount DECIMAL(20, 4) DEFAULT 0,
    total_quantity DECIMAL(15, 4) DEFAULT 0,

    -- Related documents
    transfer_order_id UUID REFERENCES transfer_orders(id) ON DELETE SET NULL,

    -- For invoicing (created when transfer is completed)
    from_org_sales_invoice_id UUID, -- Sales invoice in FROM organization
    to_org_purchase_invoice_id UUID, -- Purchase invoice in TO organization

    -- Notes
    reason TEXT,
    notes TEXT,
    internal_notes TEXT,

    -- Audit
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    approved_by UUID REFERENCES users(id) ON DELETE SET NULL,
    approved_at TIMESTAMP WITH TIME ZONE,
    shipped_by UUID REFERENCES users(id) ON DELETE SET NULL,
    shipped_at TIMESTAMP WITH TIME ZONE,
    received_by UUID REFERENCES users(id) ON DELETE SET NULL,
    received_at TIMESTAMP WITH TIME ZONE,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,

    -- Constraints
    CONSTRAINT check_different_orgs CHECK (from_organization_id != to_organization_id),
    CONSTRAINT unique_ic_transfer_number UNIQUE (tenant_id, transfer_number)
);

-- Index for faster lookups
CREATE INDEX IF NOT EXISTS idx_ic_transfers_tenant ON intercompany_transfers(tenant_id);
CREATE INDEX IF NOT EXISTS idx_ic_transfers_from_org ON intercompany_transfers(from_organization_id);
CREATE INDEX IF NOT EXISTS idx_ic_transfers_to_org ON intercompany_transfers(to_organization_id);
CREATE INDEX IF NOT EXISTS idx_ic_transfers_status ON intercompany_transfers(status);
CREATE INDEX IF NOT EXISTS idx_ic_transfers_date ON intercompany_transfers(transfer_date);

-- =====================================================
-- INTERCOMPANY TRANSFER LINES (Product Details)
-- =====================================================
CREATE TABLE IF NOT EXISTS intercompany_transfer_lines (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    intercompany_transfer_id UUID NOT NULL REFERENCES intercompany_transfers(id) ON DELETE CASCADE,

    -- Product
    product_id UUID NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
    product_variant_id UUID REFERENCES product_variants(id) ON DELETE SET NULL,

    -- Locations (optional for detailed tracking)
    from_location_id UUID REFERENCES warehouse_locations(id) ON DELETE SET NULL,
    to_location_id UUID REFERENCES warehouse_locations(id) ON DELETE SET NULL,

    -- Lot tracking
    lot_id UUID,
    lot_number VARCHAR(100),
    serial_number VARCHAR(100),

    -- Quantities
    quantity_requested DECIMAL(15, 4) NOT NULL DEFAULT 0,
    quantity_shipped DECIMAL(15, 4) DEFAULT 0,
    quantity_received DECIMAL(15, 4) DEFAULT 0,

    -- Pricing
    unit_cost DECIMAL(20, 4) DEFAULT 0, -- Original cost from source warehouse
    transfer_price DECIMAL(20, 4) DEFAULT 0, -- Price for inter-company (may include markup)
    line_total DECIMAL(20, 4) DEFAULT 0,

    -- Notes
    notes TEXT,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_ic_transfer_lines_transfer ON intercompany_transfer_lines(intercompany_transfer_id);
CREATE INDEX IF NOT EXISTS idx_ic_transfer_lines_product ON intercompany_transfer_lines(product_id);

-- =====================================================
-- INTERCOMPANY PAYMENTS (Money Transfers)
-- =====================================================
CREATE TABLE IF NOT EXISTS intercompany_payments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Payment identification
    payment_number VARCHAR(50) NOT NULL,
    payment_type VARCHAR(20) NOT NULL DEFAULT 'transfer', -- 'transfer', 'settlement', 'advance'

    -- Organizations involved
    from_organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    to_organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,

    -- Amount
    amount DECIMAL(20, 4) NOT NULL,
    currency_id UUID REFERENCES currencies(id),
    exchange_rate DECIMAL(18, 8) DEFAULT 1,
    base_currency_amount DECIMAL(20, 4), -- Amount in base currency

    -- Payment date
    payment_date DATE NOT NULL,

    -- Status: draft -> pending_approval -> approved -> completed
    status VARCHAR(20) NOT NULL DEFAULT 'draft',

    -- Payment method
    payment_method VARCHAR(30), -- 'bank_transfer', 'cash', 'internal_transfer'

    -- Bank accounts
    from_bank_account_id UUID REFERENCES bank_accounts(id) ON DELETE SET NULL,
    to_bank_account_id UUID REFERENCES bank_accounts(id) ON DELETE SET NULL,

    -- Reference
    reference VARCHAR(100),

    -- Related documents
    related_transfer_id UUID REFERENCES intercompany_transfers(id) ON DELETE SET NULL,

    -- Journal entries created
    from_org_journal_entry_id UUID REFERENCES journal_entries(id) ON DELETE SET NULL,
    to_org_journal_entry_id UUID REFERENCES journal_entries(id) ON DELETE SET NULL,

    -- Notes
    description TEXT,
    notes TEXT,

    -- Audit
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    approved_by UUID REFERENCES users(id) ON DELETE SET NULL,
    approved_at TIMESTAMP WITH TIME ZONE,
    completed_by UUID REFERENCES users(id) ON DELETE SET NULL,
    completed_at TIMESTAMP WITH TIME ZONE,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,

    -- Constraints
    CONSTRAINT check_different_payment_orgs CHECK (from_organization_id != to_organization_id),
    CONSTRAINT unique_ic_payment_number UNIQUE (tenant_id, payment_number)
);

CREATE INDEX IF NOT EXISTS idx_ic_payments_tenant ON intercompany_payments(tenant_id);
CREATE INDEX IF NOT EXISTS idx_ic_payments_from_org ON intercompany_payments(from_organization_id);
CREATE INDEX IF NOT EXISTS idx_ic_payments_to_org ON intercompany_payments(to_organization_id);
CREATE INDEX IF NOT EXISTS idx_ic_payments_status ON intercompany_payments(status);
CREATE INDEX IF NOT EXISTS idx_ic_payments_date ON intercompany_payments(payment_date);

-- =====================================================
-- INTERCOMPANY ACCOUNTS (AR/AP Tracking between companies)
-- =====================================================
CREATE TABLE IF NOT EXISTS intercompany_accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- The two organizations in the relationship
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    partner_organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,

    -- Account type: 'receivable' (they owe us) or 'payable' (we owe them)
    account_type VARCHAR(20) NOT NULL, -- 'receivable', 'payable'

    -- Currency
    currency_id UUID REFERENCES currencies(id),

    -- Current balance (positive = they owe us, for receivable; we owe them, for payable)
    balance DECIMAL(20, 4) DEFAULT 0,

    -- Last reconciliation
    last_reconciled_at TIMESTAMP WITH TIME ZONE,
    last_reconciled_by UUID REFERENCES users(id) ON DELETE SET NULL,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    -- Unique constraint: one receivable and one payable account per org pair
    CONSTRAINT unique_ic_account UNIQUE (tenant_id, organization_id, partner_organization_id, account_type)
);

CREATE INDEX IF NOT EXISTS idx_ic_accounts_tenant ON intercompany_accounts(tenant_id);
CREATE INDEX IF NOT EXISTS idx_ic_accounts_org ON intercompany_accounts(organization_id);
CREATE INDEX IF NOT EXISTS idx_ic_accounts_partner ON intercompany_accounts(partner_organization_id);

-- =====================================================
-- INTERCOMPANY ACCOUNT TRANSACTIONS (Ledger entries)
-- =====================================================
CREATE TABLE IF NOT EXISTS intercompany_account_transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    intercompany_account_id UUID NOT NULL REFERENCES intercompany_accounts(id) ON DELETE CASCADE,

    -- Transaction type
    transaction_type VARCHAR(30) NOT NULL, -- 'transfer', 'payment', 'settlement', 'adjustment'

    -- Reference to source document
    reference_type VARCHAR(30), -- 'intercompany_transfer', 'intercompany_payment'
    reference_id UUID,

    -- Amount (positive = increase balance, negative = decrease)
    amount DECIMAL(20, 4) NOT NULL,

    -- Running balance after this transaction
    balance_after DECIMAL(20, 4) NOT NULL,

    -- Date
    transaction_date DATE NOT NULL,

    -- Description
    description TEXT,

    -- Audit
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_ic_account_txns_account ON intercompany_account_transactions(intercompany_account_id);
CREATE INDEX IF NOT EXISTS idx_ic_account_txns_date ON intercompany_account_transactions(transaction_date);
CREATE INDEX IF NOT EXISTS idx_ic_account_txns_ref ON intercompany_account_transactions(reference_type, reference_id);

-- =====================================================
-- SEQUENCE FOR TRANSFER NUMBERS
-- =====================================================
CREATE SEQUENCE IF NOT EXISTS intercompany_transfer_seq START WITH 1;
CREATE SEQUENCE IF NOT EXISTS intercompany_payment_seq START WITH 1;

-- =====================================================
-- TRIGGER: Update updated_at timestamp
-- =====================================================
CREATE OR REPLACE FUNCTION update_intercompany_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trigger_ic_transfers_updated_at ON intercompany_transfers;
CREATE TRIGGER trigger_ic_transfers_updated_at
    BEFORE UPDATE ON intercompany_transfers
    FOR EACH ROW EXECUTE FUNCTION update_intercompany_updated_at();

DROP TRIGGER IF EXISTS trigger_ic_payments_updated_at ON intercompany_payments;
CREATE TRIGGER trigger_ic_payments_updated_at
    BEFORE UPDATE ON intercompany_payments
    FOR EACH ROW EXECUTE FUNCTION update_intercompany_updated_at();

DROP TRIGGER IF EXISTS trigger_ic_accounts_updated_at ON intercompany_accounts;
CREATE TRIGGER trigger_ic_accounts_updated_at
    BEFORE UPDATE ON intercompany_accounts
    FOR EACH ROW EXECUTE FUNCTION update_intercompany_updated_at();
