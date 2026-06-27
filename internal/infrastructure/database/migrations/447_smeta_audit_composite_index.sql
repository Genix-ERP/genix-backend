-- 447_smeta_audit_composite_index.sql
--
-- The "O'zgarishlar jurnali" (project audit log) query is:
--   WHERE tenant_id = $1 AND project_id = $2 [AND action = $3]
--   ORDER BY created_at DESC LIMIT 20 OFFSET N
--
-- The existing single-column indexes (project_id, created_at) and (tenant_id)
-- don't fully cover the tenant_id + project_id filter, so on a large
-- tenant-wide audit table the planner can fall back to scanning the tenant
-- index and sorting — which made the Jurnal take 1-2 minutes per page.
--
-- These composite indexes match the WHERE + ORDER BY exactly, turning both the
-- COUNT(*) and the paged SELECT into a tight index range scan.

CREATE INDEX IF NOT EXISTS idx_csa_tenant_project_created
    ON construction_smeta_audit (tenant_id, project_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_csa_tenant_project_action_created
    ON construction_smeta_audit (tenant_id, project_id, action, created_at DESC);

ANALYZE construction_smeta_audit;
