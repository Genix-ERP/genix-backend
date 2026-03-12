-- Construction acts (KS-2 / KS-3) for subcontractor work acceptance
CREATE TABLE IF NOT EXISTS construction_act (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL,
    project_id BIGINT NOT NULL REFERENCES construction_projects(id) ON DELETE CASCADE,
    subcontract_id BIGINT REFERENCES construction_subcontract(id) ON DELETE SET NULL,
    name VARCHAR(100) NOT NULL,
    act_type VARCHAR(20) NOT NULL DEFAULT 'ks2',
    period_from DATE,
    period_to DATE,
    amount_total DECIMAL(18,2) NOT NULL DEFAULT 0,
    currency VARCHAR(3) DEFAULT 'UZS',
    state VARCHAR(50) NOT NULL DEFAULT 'draft',
    approved_by UUID,
    approved_date TIMESTAMPTZ,
    rejection_reason TEXT,
    ks2_source_id BIGINT REFERENCES construction_act(id) ON DELETE SET NULL,
    notes TEXT,
    created_by UUID,
    created_date TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_date TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS construction_act_line (
    id BIGSERIAL PRIMARY KEY,
    act_id BIGINT NOT NULL REFERENCES construction_act(id) ON DELETE CASCADE,
    wbs_id BIGINT REFERENCES construction_wbs(id) ON DELETE SET NULL,
    estimate_line_id BIGINT REFERENCES construction_estimate_line(id) ON DELETE SET NULL,
    name VARCHAR(500) NOT NULL,
    uom VARCHAR(50) NOT NULL,
    quantity DECIMAL(18,4) NOT NULL,
    unit_rate DECIMAL(18,4) NOT NULL DEFAULT 0,
    total_amount DECIMAL(18,2) NOT NULL DEFAULT 0,
    sort_order INT DEFAULT 0,
    created_date TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_act_project ON construction_act(project_id);
CREATE INDEX IF NOT EXISTS idx_act_tenant ON construction_act(tenant_id);
CREATE INDEX IF NOT EXISTS idx_act_type ON construction_act(act_type);
CREATE INDEX IF NOT EXISTS idx_act_state ON construction_act(state);
CREATE INDEX IF NOT EXISTS idx_act_subcontract ON construction_act(subcontract_id);
CREATE INDEX IF NOT EXISTS idx_act_line_act ON construction_act_line(act_id);
