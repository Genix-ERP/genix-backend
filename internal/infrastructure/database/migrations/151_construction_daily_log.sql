-- Construction Daily Log (WBS-linked progress entries)
-- Each entry records work done on a specific WBS item for a specific date

CREATE TABLE IF NOT EXISTS construction_daily_log (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    project_id BIGINT NOT NULL REFERENCES construction_projects(id) ON DELETE CASCADE,
    building_id BIGINT REFERENCES construction_buildings(id) ON DELETE SET NULL,
    wbs_id BIGINT NOT NULL REFERENCES construction_wbs(id) ON DELETE CASCADE,

    date DATE NOT NULL,
    quantity_done DECIMAL(18,4) NOT NULL DEFAULT 0,
    uom VARCHAR(50) NOT NULL DEFAULT 'шт',
    cumulative_qty DECIMAL(18,4) NOT NULL DEFAULT 0,

    workers_count INT NOT NULL DEFAULT 0,
    weather VARCHAR(100),
    description TEXT,
    issues TEXT,

    reported_by UUID REFERENCES users(id),
    created_date TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_date TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_construction_daily_log_project ON construction_daily_log(project_id);
CREATE INDEX idx_construction_daily_log_wbs ON construction_daily_log(wbs_id);
CREATE INDEX idx_construction_daily_log_date ON construction_daily_log(date DESC);
CREATE INDEX idx_construction_daily_log_tenant ON construction_daily_log(tenant_id);

-- Permissions for daily log
INSERT INTO permissions (module, resource, action, description)
VALUES
    ('construction', 'daily_log', 'read', 'View construction daily logs'),
    ('construction', 'daily_log', 'create', 'Create construction daily logs'),
    ('construction', 'daily_log', 'update', 'Update construction daily logs'),
    ('construction', 'daily_log', 'delete', 'Delete construction daily logs')
ON CONFLICT (module, resource, action) DO NOTHING;
