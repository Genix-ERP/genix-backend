-- 459_ishlab_chiqarish_v2.sql — Ishlab chiqarish (Manufacturing) v2 hardening
-- (docs/production-audit.md §2, docs/production-integration-map.md)
--
-- 1. Composite indexes for the rebuilt GET /production-orders/stats endpoint
--    (single COUNT(*) FILTER KPI query, daily series on scheduled_end /
--    actual_end, overdue scan) and the period work-center load query. The
--    single-column indexes from 011 don't serve (tenant, status) or
--    (tenant, scheduled_end) scans.
-- 2. work_order_time_logs.employee_id — groundwork for the Ish haqi link
--    (integration map §6): worker_id → users(id) stays for back-compat,
--    employee_id references employees(id) so the future piece-rate payroll
--    phase can join time logs to the employee register. Nullable; no logic
--    change beyond RecordWorkOrderTime accepting it.
-- 3. Permission seed for manufacturing:cost_calculations — the
--    cost-calculation mutations were completely ungated (audit §2.11);
--    handler.go now requires create/update/delete on this resource.
--
-- Companion Go changes: internal/handler/manufacturing.go (atomic
-- stock+GL flows via applyStockDelta, source_type/source_id JE hygiene),
-- manufacturing_stats.go (new stats endpoint), work_orders.go (FG receipt
-- guard fix, org scoping), production_split.go (JE header totals),
-- handler.go (RBAC), middleware.go (resource map).

-- ---------------------------------------------------------------
-- 1. Stats / dashboard indexes
-- ---------------------------------------------------------------
CREATE INDEX IF NOT EXISTS idx_production_orders_tenant_status
    ON production_orders(tenant_id, status) WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_production_orders_tenant_sched_end
    ON production_orders(tenant_id, scheduled_end) WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_production_orders_tenant_actual_end
    ON production_orders(tenant_id, actual_end) WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_wo_time_logs_wo_start
    ON work_order_time_logs(work_order_id, start_time);

CREATE INDEX IF NOT EXISTS idx_work_orders_tenant_wc_status
    ON work_orders(tenant_id, work_center_id, status) WHERE deleted_at IS NULL;

-- ---------------------------------------------------------------
-- 2. Payroll-link groundwork (integration map §6)
-- ---------------------------------------------------------------
ALTER TABLE work_order_time_logs
    ADD COLUMN IF NOT EXISTS employee_id UUID REFERENCES employees(id);

CREATE INDEX IF NOT EXISTS idx_wo_time_logs_employee
    ON work_order_time_logs(employee_id) WHERE employee_id IS NOT NULL;

-- ---------------------------------------------------------------
-- 3. Permission rows for cost calculations (011 seed pattern)
-- ---------------------------------------------------------------
INSERT INTO permissions (id, module, resource, action, description)
SELECT gen_random_uuid(), 'manufacturing', 'cost_calculations', a.action,
       'Permission to ' || a.action || ' cost calculations'
FROM (VALUES ('read'), ('create'), ('update'), ('delete')) AS a(action)
WHERE NOT EXISTS (
    SELECT 1 FROM permissions p
    WHERE p.module = 'manufacturing'
      AND p.resource = 'cost_calculations'
      AND p.action = a.action
);
