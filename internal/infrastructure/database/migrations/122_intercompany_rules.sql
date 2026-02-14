-- =====================================================
-- INTER-COMPANY RULES (Odoo-like configuration)
-- =====================================================
-- This allows configuring automatic document sync between companies

-- Inter-company rules table
CREATE TABLE IF NOT EXISTS intercompany_rules (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Source company (the one initiating the transaction)
    source_organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,

    -- Target company (the one receiving the auto-created document)
    target_organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,

    -- Rule configuration
    rule_type VARCHAR(50) NOT NULL, -- 'sale_to_purchase', 'purchase_to_sale', 'invoice_to_bill', 'bill_to_invoice', 'transfer'
    is_active BOOLEAN DEFAULT true,

    -- Auto-sync settings
    auto_validate BOOLEAN DEFAULT false, -- Auto-validate the created document
    sync_prices BOOLEAN DEFAULT true, -- Sync prices from source document

    -- Default warehouse for receipts/deliveries
    default_warehouse_id UUID REFERENCES warehouses(id),

    -- Pricing configuration
    pricing_method VARCHAR(50) DEFAULT 'source_price', -- 'source_price', 'pricelist', 'cost_plus_markup'
    markup_percent DECIMAL(5,2) DEFAULT 0,
    pricelist_id UUID, -- Reference to pricelist if pricing_method = 'pricelist'

    -- Notes
    notes TEXT,

    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),

    -- Ensure unique rule per source-target-type combination
    UNIQUE(tenant_id, source_organization_id, target_organization_id, rule_type)
);

-- Inter-company transaction log (audit trail)
CREATE TABLE IF NOT EXISTS intercompany_transaction_logs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Rule that triggered this
    rule_id UUID REFERENCES intercompany_rules(id) ON DELETE SET NULL,

    -- Source document
    source_organization_id UUID NOT NULL REFERENCES organizations(id),
    source_document_type VARCHAR(50) NOT NULL, -- 'sale_order', 'purchase_order', 'invoice', 'bill', 'transfer'
    source_document_id UUID NOT NULL,
    source_document_number VARCHAR(100),

    -- Target document (auto-created)
    target_organization_id UUID NOT NULL REFERENCES organizations(id),
    target_document_type VARCHAR(50) NOT NULL,
    target_document_id UUID,
    target_document_number VARCHAR(100),

    -- Status
    status VARCHAR(50) DEFAULT 'pending', -- 'pending', 'created', 'validated', 'failed', 'cancelled'
    error_message TEXT,

    -- Amounts
    source_amount DECIMAL(15,2),
    target_amount DECIMAL(15,2),
    currency_id UUID,

    -- Timestamps
    processed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Inter-company document links (tracks which documents are linked)
CREATE TABLE IF NOT EXISTS intercompany_document_links (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Source document
    source_organization_id UUID NOT NULL REFERENCES organizations(id),
    source_document_type VARCHAR(50) NOT NULL,
    source_document_id UUID NOT NULL,

    -- Linked document
    linked_organization_id UUID NOT NULL REFERENCES organizations(id),
    linked_document_type VARCHAR(50) NOT NULL,
    linked_document_id UUID NOT NULL,

    -- Link type
    link_type VARCHAR(50) NOT NULL, -- 'auto_created', 'manual', 'settlement'

    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),

    -- Ensure unique links
    UNIQUE(tenant_id, source_document_type, source_document_id, linked_document_type, linked_document_id)
);

-- Add indexes
CREATE INDEX IF NOT EXISTS idx_ic_rules_tenant ON intercompany_rules(tenant_id);
CREATE INDEX IF NOT EXISTS idx_ic_rules_source_org ON intercompany_rules(source_organization_id);
CREATE INDEX IF NOT EXISTS idx_ic_rules_target_org ON intercompany_rules(target_organization_id);
CREATE INDEX IF NOT EXISTS idx_ic_rules_active ON intercompany_rules(tenant_id, is_active) WHERE is_active = true;

CREATE INDEX IF NOT EXISTS idx_ic_tx_logs_tenant ON intercompany_transaction_logs(tenant_id);
CREATE INDEX IF NOT EXISTS idx_ic_tx_logs_source ON intercompany_transaction_logs(source_organization_id, source_document_type, source_document_id);
CREATE INDEX IF NOT EXISTS idx_ic_tx_logs_target ON intercompany_transaction_logs(target_organization_id, target_document_id);
CREATE INDEX IF NOT EXISTS idx_ic_tx_logs_status ON intercompany_transaction_logs(status);

CREATE INDEX IF NOT EXISTS idx_ic_doc_links_tenant ON intercompany_document_links(tenant_id);
CREATE INDEX IF NOT EXISTS idx_ic_doc_links_source ON intercompany_document_links(source_document_type, source_document_id);
CREATE INDEX IF NOT EXISTS idx_ic_doc_links_linked ON intercompany_document_links(linked_document_type, linked_document_id);

-- Add trigger for updated_at
CREATE OR REPLACE FUNCTION update_intercompany_rules_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trigger_intercompany_rules_updated_at ON intercompany_rules;
CREATE TRIGGER trigger_intercompany_rules_updated_at
    BEFORE UPDATE ON intercompany_rules
    FOR EACH ROW
    EXECUTE FUNCTION update_intercompany_rules_updated_at();

-- Add intercompany_partner_id to organizations (for IC transactions)
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS intercompany_partner_id UUID REFERENCES organizations(id);

-- Comment explaining the schema
COMMENT ON TABLE intercompany_rules IS 'Configuration for automatic inter-company document synchronization (Odoo-like)';
COMMENT ON COLUMN intercompany_rules.rule_type IS 'Type of sync: sale_to_purchase (SO in A creates PO in B), invoice_to_bill, transfer';
COMMENT ON COLUMN intercompany_rules.auto_validate IS 'If true, auto-created documents are validated immediately';
COMMENT ON TABLE intercompany_transaction_logs IS 'Audit trail of all inter-company automatic transactions';
COMMENT ON TABLE intercompany_document_links IS 'Links between source and auto-created documents for traceability';