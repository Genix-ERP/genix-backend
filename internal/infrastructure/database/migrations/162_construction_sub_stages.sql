-- =====================================================
-- Migration 162: Construction Sub-Stages
-- =====================================================

CREATE TABLE IF NOT EXISTS construction_sub_stages (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL,
    stage_id BIGINT NOT NULL REFERENCES construction_stages(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    sub_order INT DEFAULT 0,
    status VARCHAR(50) DEFAULT 'not_started', -- not_started, in_progress, completed
    notes TEXT,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_construction_sub_stages_stage ON construction_sub_stages(tenant_id, stage_id);
