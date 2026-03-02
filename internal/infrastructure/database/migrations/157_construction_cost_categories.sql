-- =====================================================
-- Migration 157: Construction Cost Categories
-- =====================================================

CREATE TABLE IF NOT EXISTS construction_cost_categories (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL,
    code VARCHAR(50) NOT NULL,   -- materials, labor, equipment, overhead
    name VARCHAR(255) NOT NULL,
    default_debit_account_id UUID REFERENCES accounts(id),
    default_credit_account_id UUID REFERENCES accounts(id),
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(tenant_id, code)
);
