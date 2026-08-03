-- 463_mrp_engine.sql — MRP netting engine enablement (B1/B8)
--
-- The mrp_demand / mrp_supply / mrp_recommendations tables (011) get their
-- engine in internal/handler/mrp.go (POST /mrp/run, GET /mrp/recommendations,
-- POST /mrp/recommendations/:id/execute). This migration:
--   1. Grants the 011-seeded manufacturing:mrp permissions to every role
--      that already holds the matching manufacturing:production_orders
--      action (the 460 pattern — seeding permissions without granting them
--      403-locked role-based users out of the Equipment tab last time).
--   2. Indexes for the run/list hot paths: the engine clears + reads open
--      state by (tenant, status), and the recommendations list filters the
--      same way. 011 only shipped single-column indexes.

-- ---------------------------------------------------------------
-- 1. Grants (role_permissions PK = role_id + permission_id)
-- ---------------------------------------------------------------
INSERT INTO role_permissions (role_id, permission_id)
SELECT rp.role_id, np.id
FROM role_permissions rp
JOIN permissions wp ON wp.id = rp.permission_id
    AND wp.module = 'manufacturing' AND wp.resource = 'production_orders'
JOIN permissions np ON np.module = 'manufacturing'
    AND np.resource = 'mrp'
    AND np.action = wp.action
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------
-- 2. Hot-path indexes
-- ---------------------------------------------------------------
CREATE INDEX IF NOT EXISTS idx_mrp_recommendations_tenant_status
    ON mrp_recommendations(tenant_id, status);

CREATE INDEX IF NOT EXISTS idx_mrp_demand_tenant_status
    ON mrp_demand(tenant_id, status);

CREATE INDEX IF NOT EXISTS idx_mrp_supply_tenant_status
    ON mrp_supply(tenant_id, status);
