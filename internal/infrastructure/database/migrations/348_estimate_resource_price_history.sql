-- 348_estimate_resource_price_history.sql
--
-- Tracks per-resource price changes inside a project so the Smeta
-- boshqaruvi → Resurslar page can render a "price history" badge next to
-- each row. Mirrors the priceHistory map in files/Form2_Works_v2 (7).html.
--
-- A "resource" is identified by (tenant_id, project_id, name, uom,
-- resource_type) — the same key the existing /estimate-resources view
-- groups by. Edits come from the Resurslar UI and propagate to every
-- matching construction_estimate_line.unit_rate, with one history row
-- per propagation event.

CREATE TABLE IF NOT EXISTS construction_resource_price_history (
    id            BIGSERIAL PRIMARY KEY,
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    project_id    BIGINT NOT NULL,
    resource_name TEXT NOT NULL,
    uom           VARCHAR(50),
    resource_type VARCHAR(20),
    old_price     NUMERIC(20, 4) NOT NULL DEFAULT 0,
    new_price     NUMERIC(20, 4) NOT NULL,
    changed_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    changed_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    note          TEXT
);

CREATE INDEX IF NOT EXISTS idx_resource_price_history_lookup
    ON construction_resource_price_history (tenant_id, project_id, resource_name, uom);

CREATE INDEX IF NOT EXISTS idx_resource_price_history_recent
    ON construction_resource_price_history (project_id, changed_at DESC);

COMMENT ON TABLE construction_resource_price_history IS
    'Audit log of per-resource unit-price changes inside a project. Each row corresponds to one bulk-update event from the Resurslar tab.';
