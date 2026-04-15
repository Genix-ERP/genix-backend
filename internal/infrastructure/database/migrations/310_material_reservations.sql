-- Material Reservations table for construction module
-- Tracks reservation requests from construction substages against inventory

CREATE TABLE IF NOT EXISTS material_reservations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id),
    organization_id UUID,

    -- Construction references
    project_id      BIGINT NOT NULL,
    stage_id        BIGINT NOT NULL,
    substage_id     BIGINT NOT NULL,

    -- Product / inventory
    product_id      UUID NOT NULL REFERENCES products(id),
    warehouse_id    UUID,
    quantity        DECIMAL(20,4) NOT NULL DEFAULT 0,
    unit            VARCHAR(50),
    unit_cost       DECIMAL(20,4) NOT NULL DEFAULT 0,
    total_cost      DECIMAL(20,4) NOT NULL DEFAULT 0,

    -- Status workflow: pending → approved / rejected
    status          VARCHAR(20) NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','approved','rejected','cancelled')),

    -- Users
    requested_by    UUID REFERENCES users(id),
    approved_by     UUID REFERENCES users(id),

    -- Accounting (filled on approval)
    expense_line_id BIGINT,
    journal_entry_id UUID,

    notes           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    approved_at     TIMESTAMPTZ,
    deleted_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_material_reservations_tenant
    ON material_reservations(tenant_id);
CREATE INDEX IF NOT EXISTS idx_material_reservations_project
    ON material_reservations(project_id);
CREATE INDEX IF NOT EXISTS idx_material_reservations_substage
    ON material_reservations(substage_id);
CREATE INDEX IF NOT EXISTS idx_material_reservations_product
    ON material_reservations(product_id);
CREATE INDEX IF NOT EXISTS idx_material_reservations_status
    ON material_reservations(status);
CREATE INDEX IF NOT EXISTS idx_material_reservations_tenant_status
    ON material_reservations(tenant_id, status);
