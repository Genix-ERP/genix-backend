-- 457_fa_maintenance.sql
--
-- Texnik xizmat (maintenance) for the unified fa_assets register — the one
-- legacy capability the 453 rebuild deferred (docs/aktivlar-changelog.md
-- follow-ups). Types follow the legacy semantics:
--   regular_to / minor_repair  -> pure expense (Dt dept expense / Kt payment)
--   capital_repair / modernization -> capitalized (Dt asset account / Kt
--     payment), asset cost += cost, optional useful-life extension; future
--     accruals recompute prospectively exactly like /change-params.
CREATE TABLE IF NOT EXISTS fa_maintenance (
    id                   UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id            UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    asset_id             UUID NOT NULL REFERENCES fa_assets(id) ON DELETE CASCADE,
    maintenance_type     TEXT NOT NULL CHECK (maintenance_type IN ('regular_to','minor_repair','capital_repair','modernization')),
    service_date         DATE NOT NULL,
    cost                 NUMERIC(18,2) NOT NULL DEFAULT 0 CHECK (cost >= 0),
    payment_method       TEXT NOT NULL DEFAULT 'credit',   -- 'cash' | 'bank' | 'credit'
    life_extension_months INTEGER NOT NULL DEFAULT 0 CHECK (life_extension_months >= 0),
    -- Snapshots for the audit trail (before -> after)
    cost_before          NUMERIC(18,2),
    cost_after           NUMERIC(18,2),
    life_before          INTEGER,
    life_after           INTEGER,
    performed_by         TEXT,
    doc_number           TEXT,
    description          TEXT,
    next_service_date    DATE,
    journal_entry_id     UUID,
    created_by           UUID REFERENCES users(id),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_fa_maintenance_asset  ON fa_maintenance(asset_id);
CREATE INDEX IF NOT EXISTS idx_fa_maintenance_tenant ON fa_maintenance(tenant_id);
