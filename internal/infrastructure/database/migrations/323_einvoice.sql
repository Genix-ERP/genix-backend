-- Migration 323: E-invoice (elektron hisob-faktura) integration
-- Reference: TT Buxgalteriya ERP §8.2 — E-invoice / soliq organlari
--
-- Stores both incoming invoices (we receive from suppliers) and outgoing
-- (we send to customers). Provider can be didox | faktura | soliq — the
-- provider-specific adapter is in Go.

CREATE TABLE IF NOT EXISTS einvoices (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    provider VARCHAR(32) NOT NULL DEFAULT 'didox',     -- didox | faktura | soliq
    direction VARCHAR(10) NOT NULL,                    -- incoming | outgoing
    provider_doc_id VARCHAR(128),                      -- GUID from provider
    facture_type VARCHAR(32),                          -- standard | adjustment | correction
    document_number VARCHAR(64),
    document_date DATE,

    -- Parties
    seller_tin VARCHAR(32),
    seller_name VARCHAR(255),
    buyer_tin VARCHAR(32),
    buyer_name VARCHAR(255),

    -- Money
    total_amount DECIMAL(20, 4) NOT NULL DEFAULT 0,
    tax_amount DECIMAL(20, 4) NOT NULL DEFAULT 0,
    total_with_tax DECIMAL(20, 4) NOT NULL DEFAULT 0,
    currency CHAR(3) NOT NULL DEFAULT 'UZS',

    -- Local linkage
    linked_purchase_invoice_id UUID,
    linked_sales_invoice_id UUID,
    linked_journal_entry_id UUID,
    linked_contact_id UUID,

    -- Workflow
    status VARCHAR(20) NOT NULL DEFAULT 'received',
        -- received | pending_approval | approved | rejected | sent | confirmed | cancelled
    error_message TEXT,
    raw_xml TEXT,
    raw_pdf_url TEXT,

    -- Audit
    received_at TIMESTAMPTZ,
    approved_at TIMESTAMPTZ,
    approved_by UUID,
    rejected_at TIMESTAMPTZ,
    rejection_reason TEXT,
    sent_at TIMESTAMPTZ,
    confirmed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_einvoices_tenant_dir_status
    ON einvoices (tenant_id, direction, status);
CREATE INDEX IF NOT EXISTS idx_einvoices_provider_doc
    ON einvoices (provider, provider_doc_id)
    WHERE provider_doc_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_einvoices_provider_doc
    ON einvoices (tenant_id, provider, provider_doc_id)
    WHERE provider_doc_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS einvoice_lines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    einvoice_id UUID NOT NULL REFERENCES einvoices(id) ON DELETE CASCADE,
    line_number INT NOT NULL,
    product_code VARCHAR(64),
    description TEXT,
    quantity DECIMAL(20, 6) NOT NULL DEFAULT 0,
    unit VARCHAR(16),
    unit_price DECIMAL(20, 4) NOT NULL DEFAULT 0,
    line_amount DECIMAL(20, 4) NOT NULL DEFAULT 0,
    tax_rate DECIMAL(5, 2),
    tax_amount DECIMAL(20, 4) NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_einvoice_lines_invoice ON einvoice_lines (einvoice_id);

-- Provider credential storage (symmetric-encrypted at app layer)
CREATE TABLE IF NOT EXISTS einvoice_provider_credentials (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    provider VARCHAR(32) NOT NULL,
    endpoint_url TEXT,
    login VARCHAR(255),
    encrypted_password TEXT,
    certificate_id VARCHAR(255),
    is_active BOOLEAN NOT NULL DEFAULT true,
    last_ping_at TIMESTAMPTZ,
    last_ping_status VARCHAR(20),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, organization_id, provider)
);

COMMENT ON TABLE einvoices IS
    'TT Buxgalteriya §8.2: electronic invoices exchanged with didox.uz / faktura / soliq.uz.';
COMMENT ON TABLE einvoice_provider_credentials IS
    'Per-organization credentials for e-invoice providers. Password stored encrypted at app layer.';
