-- Migration 322: Bank statement import tracking
-- Reference: TT Buxgalteriya ERP §8.1 — Bank-mijoz integration

CREATE TABLE IF NOT EXISTS bank_statement_imports (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    bank_account_id UUID,
    format VARCHAR(32) NOT NULL DEFAULT '1c_txt',  -- 1c_txt | camt053 | mt940 | csv
    file_name VARCHAR(255),
    file_size INT,
    raw_content TEXT,                               -- parsed content snapshot (first 500KB)
    statement_date DATE,
    opening_balance DECIMAL(20, 4),
    closing_balance DECIMAL(20, 4),
    total_credit DECIMAL(20, 4) NOT NULL DEFAULT 0,  -- money in
    total_debit DECIMAL(20, 4) NOT NULL DEFAULT 0,   -- money out
    transaction_count INT NOT NULL DEFAULT 0,
    matched_count INT NOT NULL DEFAULT 0,
    unmatched_count INT NOT NULL DEFAULT 0,
    status VARCHAR(20) NOT NULL DEFAULT 'imported',  -- imported | reconciled | failed
    error_message TEXT,
    imported_by UUID,
    imported_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reconciled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_bank_statement_imports_tenant_date
    ON bank_statement_imports (tenant_id, statement_date DESC);

CREATE TABLE IF NOT EXISTS bank_statement_transactions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    import_id UUID NOT NULL REFERENCES bank_statement_imports(id) ON DELETE CASCADE,
    line_number INT NOT NULL,
    doc_number VARCHAR(64),
    doc_date DATE,
    counterparty_name VARCHAR(255),
    counterparty_inn VARCHAR(32),
    counterparty_account VARCHAR(32),
    bank_name VARCHAR(255),
    bank_bic VARCHAR(20),
    amount DECIMAL(20, 4) NOT NULL,
    direction VARCHAR(10) NOT NULL,        -- in | out
    purpose TEXT,
    matched_contact_id UUID,
    matched_journal_entry_id UUID,
    match_status VARCHAR(20) NOT NULL DEFAULT 'unmatched',
        -- unmatched | suggested | matched | ignored
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_bst_import ON bank_statement_transactions (import_id);
CREATE INDEX IF NOT EXISTS idx_bst_match_status ON bank_statement_transactions (match_status);

COMMENT ON TABLE bank_statement_imports IS
    'TT Buxgalteriya §8.1: tracks bank statement files imported via the bank-mijoz flow.';

COMMENT ON TABLE bank_statement_transactions IS
    'Individual transactions parsed from bank statements. Unmatched rows are surfaced to the buxgalter for manual assignment.';
