-- Construction Estimates (Versioned Smeta System)
-- Linked to WBS for hierarchical budget tracking

-- Estimate header (versions of the project budget)
CREATE TABLE IF NOT EXISTS construction_estimate (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    project_id BIGINT NOT NULL REFERENCES construction_projects(id) ON DELETE CASCADE,
    version INT NOT NULL DEFAULT 1,
    name VARCHAR(255) NOT NULL,
    state VARCHAR(20) NOT NULL DEFAULT 'draft' CHECK (state IN ('draft', 'approved', 'archived')),
    is_current BOOLEAN NOT NULL DEFAULT false,

    -- Cost modifiers (percentages)
    overhead_pct DECIMAL(5,2) NOT NULL DEFAULT 0,
    profit_pct DECIMAL(5,2) NOT NULL DEFAULT 0,
    vat_pct DECIMAL(5,2) NOT NULL DEFAULT 0,

    -- Calculated totals (updated by application)
    amount_direct DECIMAL(18,2) NOT NULL DEFAULT 0,
    amount_total DECIMAL(18,2) NOT NULL DEFAULT 0,

    -- Approval
    approved_by UUID REFERENCES users(id),
    approved_date TIMESTAMPTZ,

    created_by UUID REFERENCES users(id),
    created_date TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_date TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_construction_estimate_project ON construction_estimate(project_id);
CREATE INDEX idx_construction_estimate_tenant ON construction_estimate(tenant_id);
CREATE UNIQUE INDEX idx_construction_estimate_version ON construction_estimate(project_id, version);

-- Estimate line items (linked to WBS)
CREATE TABLE IF NOT EXISTS construction_estimate_line (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    estimate_id BIGINT NOT NULL REFERENCES construction_estimate(id) ON DELETE CASCADE,
    wbs_id BIGINT REFERENCES construction_wbs(id) ON DELETE SET NULL,

    name VARCHAR(500) NOT NULL,
    uom VARCHAR(50) NOT NULL DEFAULT 'шт',
    quantity DECIMAL(18,4) NOT NULL DEFAULT 0,

    -- Cost rates per unit
    material_rate DECIMAL(18,2) NOT NULL DEFAULT 0,
    labor_rate DECIMAL(18,2) NOT NULL DEFAULT 0,
    equipment_rate DECIMAL(18,2) NOT NULL DEFAULT 0,

    -- Calculated fields (updated by application)
    unit_rate DECIMAL(18,2) NOT NULL DEFAULT 0,
    total_amount DECIMAL(18,2) NOT NULL DEFAULT 0,

    -- Actual tracking
    actual_amount DECIMAL(18,2) NOT NULL DEFAULT 0,

    sort_order INT NOT NULL DEFAULT 0,
    created_date TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_date TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_construction_estimate_line_estimate ON construction_estimate_line(estimate_id);
CREATE INDEX idx_construction_estimate_line_wbs ON construction_estimate_line(wbs_id);

-- Permissions for estimates
INSERT INTO permissions (module, resource, action, description)
VALUES
    ('construction', 'estimate', 'read', 'View construction estimates'),
    ('construction', 'estimate', 'create', 'Create construction estimates'),
    ('construction', 'estimate', 'update', 'Update construction estimates'),
    ('construction', 'estimate', 'delete', 'Delete construction estimates')
ON CONFLICT (module, resource, action) DO NOTHING;
