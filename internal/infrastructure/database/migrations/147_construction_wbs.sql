-- =====================================================
-- CONSTRUCTION MODULE - WORK BREAKDOWN STRUCTURE (WBS)
-- Migration: 147_construction_wbs.sql
-- The WBS is the CENTRAL entity for the construction module.
-- Daily logs, estimates, materials, acts ALL link to WBS.
-- =====================================================

-- 1. WBS (Work Breakdown Structure) - Hierarchical work items
CREATE TABLE IF NOT EXISTS construction_wbs (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    project_id BIGINT NOT NULL REFERENCES construction_projects(id) ON DELETE CASCADE,
    building_id BIGINT REFERENCES construction_buildings(id) ON DELETE SET NULL,
    parent_id BIGINT REFERENCES construction_wbs(id) ON DELETE CASCADE,

    -- Identification
    code VARCHAR(100) NOT NULL,
    name VARCHAR(500) NOT NULL,
    description TEXT,

    -- Planned dates
    date_start_plan DATE,
    date_end_plan DATE,
    date_start_actual DATE,
    date_end_actual DATE,

    -- Budget & Progress
    budget_amount DECIMAL(18,2) DEFAULT 0,
    progress DECIMAL(5,2) DEFAULT 0,

    -- Ordering & assignment
    sort_order INTEGER DEFAULT 0,
    responsible_id UUID REFERENCES employees(id),

    -- Soft delete
    is_active BOOLEAN DEFAULT true,

    created_date TIMESTAMP DEFAULT NOW(),
    updated_date TIMESTAMP DEFAULT NOW(),

    UNIQUE(project_id, code)
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_construction_wbs_project ON construction_wbs(project_id);
CREATE INDEX IF NOT EXISTS idx_construction_wbs_building ON construction_wbs(building_id);
CREATE INDEX IF NOT EXISTS idx_construction_wbs_parent ON construction_wbs(parent_id);
CREATE INDEX IF NOT EXISTS idx_construction_wbs_active ON construction_wbs(project_id, is_active);

-- Permissions
INSERT INTO permissions (module, resource, action, description) VALUES
('construction', 'wbs', 'read', 'View work breakdown structure'),
('construction', 'wbs', 'create', 'Create WBS items'),
('construction', 'wbs', 'update', 'Edit WBS items'),
('construction', 'wbs', 'delete', 'Delete WBS items')
ON CONFLICT (module, resource, action) DO NOTHING;
