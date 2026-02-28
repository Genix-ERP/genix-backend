-- Migration: 125_reconciliation_acts.sql
-- Description: Create reconciliation acts and lines tables for partner reconciliation
-- Date: 2026-02-14

-- ============================================
-- RECONCILIATION ACTS (Akt sverka)
-- ============================================
CREATE TABLE IF NOT EXISTS reconciliation_acts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    partner_id UUID NOT NULL REFERENCES contacts(id) ON DELETE RESTRICT,
    period_start DATE NOT NULL,
    period_end DATE NOT NULL,
    opening_balance DECIMAL(20, 4) DEFAULT 0,
    our_debit_total DECIMAL(20, 4) DEFAULT 0,
    our_credit_total DECIMAL(20, 4) DEFAULT 0,
    our_balance DECIMAL(20, 4) DEFAULT 0,
    partner_debit_total DECIMAL(20, 4) DEFAULT 0,
    partner_credit_total DECIMAL(20, 4) DEFAULT 0,
    partner_balance DECIMAL(20, 4) DEFAULT 0,
    discrepancy DECIMAL(20, 4) DEFAULT 0,
    status VARCHAR(20) DEFAULT 'draft', -- draft, sent, confirmed, disputed
    notes TEXT,
    created_by UUID REFERENCES users(id),
    confirmed_by UUID REFERENCES users(id),
    confirmed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_reconciliation_acts_tenant ON reconciliation_acts(tenant_id, deleted_at);
CREATE INDEX IF NOT EXISTS idx_reconciliation_acts_partner ON reconciliation_acts(partner_id, deleted_at);
CREATE INDEX IF NOT EXISTS idx_reconciliation_acts_period ON reconciliation_acts(tenant_id, period_start, period_end);
CREATE INDEX IF NOT EXISTS idx_reconciliation_acts_status ON reconciliation_acts(tenant_id, status);

-- ============================================
-- RECONCILIATION LINES (Akt sverka qatorlari)
-- ============================================
CREATE TABLE IF NOT EXISTS reconciliation_lines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    act_id UUID NOT NULL REFERENCES reconciliation_acts(id) ON DELETE CASCADE,
    line_date DATE NOT NULL,
    document VARCHAR(255),
    description TEXT,
    our_debit DECIMAL(20, 4) DEFAULT 0,
    our_credit DECIMAL(20, 4) DEFAULT 0,
    partner_debit DECIMAL(20, 4) DEFAULT 0,
    partner_credit DECIMAL(20, 4) DEFAULT 0,
    line_number INTEGER DEFAULT 0,
    discrepancy DECIMAL(20, 4) DEFAULT 0,
    is_disputed BOOLEAN DEFAULT false,
    dispute_note TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_reconciliation_lines_act ON reconciliation_lines(act_id);
CREATE INDEX IF NOT EXISTS idx_reconciliation_lines_date ON reconciliation_lines(act_id, line_date);
