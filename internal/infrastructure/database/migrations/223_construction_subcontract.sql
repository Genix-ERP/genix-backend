-- Subcontractor management for construction projects
CREATE TABLE IF NOT EXISTS construction_subcontract (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL,
    project_id BIGINT NOT NULL REFERENCES construction_projects(id) ON DELETE CASCADE,
    partner_id UUID,
    name VARCHAR(100) NOT NULL,
    work_description TEXT,
    amount DECIMAL(18,2) NOT NULL DEFAULT 0,
    currency VARCHAR(3) DEFAULT 'UZS',
    start_date DATE,
    end_date DATE,
    retention_pct DECIMAL(5,2) DEFAULT 0,
    state VARCHAR(50) NOT NULL DEFAULT 'draft',
    rating DECIMAL(3,1) DEFAULT 0,
    contact_person VARCHAR(255),
    contact_phone VARCHAR(50),
    notes TEXT,
    created_by UUID,
    created_date TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_date TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS construction_subcontract_wbs (
    id BIGSERIAL PRIMARY KEY,
    subcontract_id BIGINT NOT NULL REFERENCES construction_subcontract(id) ON DELETE CASCADE,
    wbs_id BIGINT NOT NULL REFERENCES construction_wbs(id) ON DELETE CASCADE,
    UNIQUE(subcontract_id, wbs_id)
);

CREATE INDEX IF NOT EXISTS idx_subcontract_project ON construction_subcontract(project_id);
CREATE INDEX IF NOT EXISTS idx_subcontract_tenant ON construction_subcontract(tenant_id);
CREATE INDEX IF NOT EXISTS idx_subcontract_state ON construction_subcontract(state);
CREATE INDEX IF NOT EXISTS idx_subcontract_wbs_sub ON construction_subcontract_wbs(subcontract_id);
