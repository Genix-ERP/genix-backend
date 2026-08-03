-- 461_qurilish_hygiene.sql
-- Qurilish (Construction) v2 hygiene, phase 1 — docs/construction-audit.md /
-- docs/construction-roadmap.md (S2, safe subset).
--
-- Contents:
--   1. construction_projects.status gets a CHECK backstop (legacy values
--      remapped first) — the free-form status write-path is closed at the
--      data layer like 449 (Xarid) / 451 (Savdo) did for their modules.
--   2. project_files.tenant_id VARCHAR(100) → UUID + FK + index. It was the
--      only tenant column in the ERP typed as VARCHAR, so tenant filters on
--      files could never use an index or referential integrity. Rows whose
--      tenant_id does not parse as a UUID are detached (NULLed) rather than
--      dropped — none should exist, but a migration must not destroy data.
--   3. Permission seed for the SINGULAR keys the routes actually gate on
--      (construction:project:*, construction:smeta:*, construction:reports:read).
--      Until now only the plural 'projects' spelling existed in `permissions`,
--      so role_permissions could never grant what the routes check — access
--      worked only via the employee_module_permissions wildcard expansion.
--   4. Indexes for the portfolio list/stats paths.
--
-- Deliberately NOT here (needs an explicit product decision, see roadmap):
-- dropping the dead 111-era tables (construction_work_progress,
-- construction_cost_tracking, construction_material_issues,
-- construction_vendor_payments) and the legacy smeta_sections/smeta_items pair.

-- ── 1. Status vocabulary ─────────────────────────────────────────────────
-- Legacy 'active' (the pre-v2 UI's word for a running project — the dominant
-- real value in existing data) maps to 'in_progress'; anything else unknown
-- falls back to 'draft'.
UPDATE construction_projects SET status = 'in_progress' WHERE status = 'active';

UPDATE construction_projects
SET status = 'draft'
WHERE status IS NULL
   OR status NOT IN ('draft', 'planning', 'approved', 'in_progress', 'on_hold', 'completed', 'cancelled');

ALTER TABLE construction_projects
    DROP CONSTRAINT IF EXISTS chk_construction_project_status;
ALTER TABLE construction_projects
    ADD CONSTRAINT chk_construction_project_status
    CHECK (status IN ('draft', 'planning', 'approved', 'in_progress', 'on_hold', 'completed', 'cancelled'));

-- ── 2. project_files.tenant_id → UUID ────────────────────────────────────
-- Detach rows with junk tenant ids (defensive; expected count: 0).
UPDATE project_files
SET tenant_id = NULL
WHERE tenant_id IS NOT NULL
  AND tenant_id::text !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$';

ALTER TABLE project_files
    ALTER COLUMN tenant_id DROP NOT NULL;
ALTER TABLE project_files
    ALTER COLUMN tenant_id TYPE UUID USING NULLIF(tenant_id::text, '')::uuid;
ALTER TABLE project_files
    DROP CONSTRAINT IF EXISTS fk_project_files_tenant;
ALTER TABLE project_files
    ADD CONSTRAINT fk_project_files_tenant
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_project_files_tenant_project
    ON project_files(tenant_id, project_id);

-- ── 3. Permission seed — singular keys used by the routes ────────────────
INSERT INTO permissions (module, resource, action, description) VALUES
('construction', 'project', 'read',   'View construction projects'),
('construction', 'project', 'create', 'Create construction projects'),
('construction', 'project', 'update', 'Edit construction projects'),
('construction', 'project', 'delete', 'Delete construction projects'),
('construction', 'smeta',   'read',   'View smeta sections/items'),
('construction', 'smeta',   'create', 'Create smeta sections/items'),
('construction', 'smeta',   'update', 'Edit smeta sections/items'),
('construction', 'smeta',   'delete', 'Delete smeta sections/items'),
('construction', 'reports', 'read',   'View construction daily/photo reports')
ON CONFLICT (module, resource, action) DO NOTHING;

-- ── 4. Indexes for portfolio list + stats paths ──────────────────────────
-- Live-project scans (list page, stats, director panel block).
CREATE INDEX IF NOT EXISTS idx_construction_projects_tenant_live
    ON construction_projects(tenant_id)
    WHERE deleted_at IS NULL;

-- Approved object-cost rollups (stats actual_total / per-project fakt).
CREATE INDEX IF NOT EXISTS idx_cel_tenant_project_status
    ON construction_expense_lines(tenant_id, project_id, status)
    WHERE deleted_at IS NULL;
