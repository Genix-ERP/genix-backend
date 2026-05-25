-- 358_resource_topup.sql
--
-- Resource top-ups — when a foreman orders MORE of a material than the
-- smeta originally planned (e.g. they thought 71.4 m³ of concrete would
-- cover the slab but ended up needing 76.4), the additional purchase
-- often happens at a different price than the original. Recording the
-- shortfall as a separate row keeps the original smeta plan intact
-- (immutable for audit) while still letting the cost recalculation
-- account for the real spend.
--
-- Each top-up belongs to a specific construction_estimate_line (the
-- resource sub-row that's being topped up) and carries:
--   • extra_quantity  — how much more was ordered
--   • new_price       — unit price at the time of the top-up order
--   • ordered_at      — when the order was placed (auditable)
--   • note            — free-text reason / supplier hint
--
-- The total cost of the line then becomes:
--   line.quantity × line.unit_rate                                      (planned)
-- + Σ (topup.extra_quantity × topup.new_price)                          (top-ups)
--
-- Multiple top-ups per line are allowed (you might re-order three times
-- if the shortfall is gradual), so this is a one-to-many relation.

CREATE TABLE IF NOT EXISTS construction_resource_topup (
    id               BIGSERIAL PRIMARY KEY,
    tenant_id        UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    estimate_line_id BIGINT NOT NULL REFERENCES construction_estimate_line(id) ON DELETE CASCADE,
    extra_quantity   NUMERIC(20, 6) NOT NULL CHECK (extra_quantity > 0),
    new_price        NUMERIC(20, 4) NOT NULL CHECK (new_price >= 0),
    ordered_at       DATE NOT NULL DEFAULT CURRENT_DATE,
    note             TEXT,
    created_by       UUID,
    created_date     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Hot path: load all top-ups for an estimate's lines in one query
-- when rendering the Smeta Management tab or the Form 2 preview.
CREATE INDEX IF NOT EXISTS idx_topup_estimate_line
    ON construction_resource_topup (estimate_line_id);

-- Tenant scoping for the rare cross-tenant queries (cleanup jobs,
-- migrations).
CREATE INDEX IF NOT EXISTS idx_topup_tenant
    ON construction_resource_topup (tenant_id);
