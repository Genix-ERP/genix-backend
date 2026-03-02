-- =====================================================
-- CONSTRUCTION MODULE - ACTIVITY LOG (Audit Trail)
-- Migration: 148_construction_activity_log.sql
-- Tracks all actions within a construction project
-- =====================================================

CREATE TABLE IF NOT EXISTS construction_activity_log (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    project_id BIGINT NOT NULL REFERENCES construction_projects(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id),

    -- Action details
    action_type VARCHAR(50) NOT NULL, -- progress, estimate, act, material, photo, team, status_change
    description TEXT NOT NULL,

    -- Related entity (polymorphic reference)
    related_model VARCHAR(100),  -- 'DailyLog', 'Estimate', 'Act', 'Photo', etc.
    related_id BIGINT,

    -- Additional metadata
    metadata JSONB,

    created_at TIMESTAMP DEFAULT NOW()
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_construction_activity_project ON construction_activity_log(project_id);
CREATE INDEX IF NOT EXISTS idx_construction_activity_type ON construction_activity_log(action_type);
CREATE INDEX IF NOT EXISTS idx_construction_activity_date ON construction_activity_log(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_construction_activity_user ON construction_activity_log(user_id);
