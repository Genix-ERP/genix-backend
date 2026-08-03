-- 465_ishlab_chiqarish_wave2.sql — Ishlab chiqarish strategic wave 2
-- (B4 costing unification is Go-only; this file carries B5/B6/B7 schema)
--
-- B5 — quality capture: quality_checks (011) becomes a written table via
--      POST /work-orders/:id/quality-check. Role grant for the 011-seeded
--      manufacturing:quality_checks permissions (460 pattern — seeding
--      without granting locked role users out of Equipment last time).
-- B6 — ishbay (piece-rate) groundwork: bom_operations.piece_rate (pay per
--      good unit for that operation) + production_piecework, the HR handoff
--      register CompleteWorkOrder writes into. NO payroll_entries writes —
--      payroll consumes this table in its own phase.
-- B7 — asset links: work_centers.asset_id / manufacturing_equipment.asset_id
--      → fa_assets (integration map §6; no forced data migration).

-- ---------------------------------------------------------------
-- B5: quality_checks role grants + period index
-- ---------------------------------------------------------------
INSERT INTO role_permissions (role_id, permission_id)
SELECT rp.role_id, np.id
FROM role_permissions rp
JOIN permissions wp ON wp.id = rp.permission_id
    AND wp.module = 'manufacturing' AND wp.resource = 'production_orders'
JOIN permissions np ON np.module = 'manufacturing'
    AND np.resource = 'quality_checks'
    AND np.action = wp.action
ON CONFLICT DO NOTHING;

CREATE INDEX IF NOT EXISTS idx_quality_checks_tenant_date
    ON quality_checks(tenant_id, inspection_date) WHERE deleted_at IS NULL;

-- ---------------------------------------------------------------
-- B6: piece-rate on operations + the piecework handoff register
-- ---------------------------------------------------------------
ALTER TABLE bom_operations ADD COLUMN IF NOT EXISTS piece_rate NUMERIC(15,2);

COMMENT ON COLUMN bom_operations.piece_rate IS
    'Ishbay: pay per good unit for this operation. NULL/0 = no piece rate.';

CREATE TABLE IF NOT EXISTS production_piecework (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    organization_id UUID REFERENCES organizations(id),
    work_order_id UUID NOT NULL REFERENCES work_orders(id),
    production_order_id UUID REFERENCES production_orders(id),
    employee_id UUID NOT NULL REFERENCES employees(id),
    operation_name VARCHAR(255),
    good_quantity DECIMAL(15,4) NOT NULL,
    piece_rate NUMERIC(15,2) NOT NULL,
    amount NUMERIC(15,2) NOT NULL,
    work_date DATE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    -- One piecework row per operator per work order — the double-post guard
    -- for CompleteWorkOrder retries.
    UNIQUE (work_order_id, employee_id)
);

CREATE INDEX IF NOT EXISTS idx_piecework_tenant_date
    ON production_piecework(tenant_id, work_date);
CREATE INDEX IF NOT EXISTS idx_piecework_employee
    ON production_piecework(employee_id);

-- ---------------------------------------------------------------
-- B7: asset links (fa_assets is THE register — migration 453-457)
-- ---------------------------------------------------------------
ALTER TABLE work_centers
    ADD COLUMN IF NOT EXISTS asset_id UUID REFERENCES fa_assets(id) ON DELETE SET NULL;
ALTER TABLE manufacturing_equipment
    ADD COLUMN IF NOT EXISTS asset_id UUID REFERENCES fa_assets(id) ON DELETE SET NULL;
