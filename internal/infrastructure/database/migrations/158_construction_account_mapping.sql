-- =====================================================
-- Migration 158: Construction Account Mapping
-- Maps TZ account codes to actual chart-of-accounts entries
-- =====================================================

CREATE TABLE IF NOT EXISTS construction_account_mapping (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL,
    -- mapping_key values: wip_0810, fixed_assets_0100, payables_6010,
    --                      payroll_6710, depreciation_0200, cash_5010, bank_5110
    mapping_key VARCHAR(50) NOT NULL,
    account_id UUID NOT NULL REFERENCES accounts(id),
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(tenant_id, mapping_key)
);
