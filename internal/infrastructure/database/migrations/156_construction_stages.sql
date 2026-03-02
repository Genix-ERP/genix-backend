-- =====================================================
-- Migration 156: Construction Stages
-- =====================================================

CREATE TABLE IF NOT EXISTS construction_stages (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL,
    project_id BIGINT NOT NULL REFERENCES construction_projects(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    stage_order INT DEFAULT 0,
    status VARCHAR(50) DEFAULT 'not_started', -- not_started, in_progress, completed
    planned_budget DECIMAL(15,2) DEFAULT 0,
    planned_start DATE,
    planned_end DATE,
    actual_start DATE,
    actual_end DATE,
    notes TEXT,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_construction_stages_project ON construction_stages(tenant_id, project_id);
