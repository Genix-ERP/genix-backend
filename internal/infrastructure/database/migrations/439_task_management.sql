-- Migration 439: Vazifalar (task management) module — schema
--
-- Why needed:
--   The Loyihalar (projects) module is being rebuilt as a proper task manager
--   ("Vazifalar"). The old schema (projects/project_tasks/...) cannot express
--   boards, dynamic columns, card positions or multi-assignee tasks.
--   Data carry-over happens in 440; legacy tables are dropped in 441.
--
-- What it does:
--   1. Creates task_boards / task_columns / tasks / task_assignees /
--      task_checklist_items / task_comments / task_activity / task_links.
--      (Task attachments reuse the existing polymorphic `attachments` table
--      with entity_type = 'task' — no new table needed.)
--   2. Seeds tasks:{board,column,task}:{read,create,update,delete} permissions.
--
-- Idempotent — every statement is guarded with IF NOT EXISTS / NOT EXISTS.

-- ── Boards ──────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS task_boards (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    color VARCHAR(20) DEFAULT 'blue',
    icon VARCHAR(50) DEFAULT 'kanban',
    created_by UUID REFERENCES users(id),
    archived_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_task_boards_tenant ON task_boards(tenant_id);
CREATE INDEX IF NOT EXISTS idx_task_boards_tenant_org ON task_boards(tenant_id, organization_id);

-- ── Columns (fully dynamic, per board) ──────────────────────────────────────
CREATE TABLE IF NOT EXISTS task_columns (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    board_id UUID NOT NULL REFERENCES task_boards(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    color VARCHAR(20) DEFAULT 'gray',
    position INTEGER NOT NULL DEFAULT 0,
    is_done_column BOOLEAN NOT NULL DEFAULT false,
    wip_limit INTEGER CHECK (wip_limit IS NULL OR wip_limit > 0),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_task_columns_board ON task_columns(board_id, position);
CREATE INDEX IF NOT EXISTS idx_task_columns_tenant ON task_columns(tenant_id);

-- ── Tasks ───────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS tasks (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    board_id UUID NOT NULL REFERENCES task_boards(id) ON DELETE CASCADE,
    column_id UUID NOT NULL REFERENCES task_columns(id),
    title VARCHAR(500) NOT NULL,
    description TEXT, -- markdown
    priority VARCHAR(20) NOT NULL DEFAULT 'normal'
        CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
    due_date DATE,
    start_date DATE,
    position INTEGER NOT NULL DEFAULT 0,
    completed_at TIMESTAMP WITH TIME ZONE,
    archived_at TIMESTAMP WITH TIME ZONE,
    -- stamped by the overdue scanner so each task notifies exactly once
    overdue_notified_at TIMESTAMP WITH TIME ZONE,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_tasks_board_column_pos ON tasks(board_id, column_id, position);
CREATE INDEX IF NOT EXISTS idx_tasks_tenant ON tasks(tenant_id);
CREATE INDEX IF NOT EXISTS idx_tasks_overdue ON tasks(tenant_id, due_date)
    WHERE completed_at IS NULL AND archived_at IS NULL;

-- ── Assignees (many-to-many → HR employees) ────────────────────────────────
CREATE TABLE IF NOT EXISTS task_assignees (
    task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    assigned_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (task_id, employee_id)
);
CREATE INDEX IF NOT EXISTS idx_task_assignees_employee ON task_assignees(employee_id);
CREATE INDEX IF NOT EXISTS idx_task_assignees_tenant_employee ON task_assignees(tenant_id, employee_id);

-- ── Checklist ───────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS task_checklist_items (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    title VARCHAR(500) NOT NULL,
    is_done BOOLEAN NOT NULL DEFAULT false,
    position INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_task_checklist_task ON task_checklist_items(task_id, position);

-- ── Comments ────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS task_comments (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    author_id UUID REFERENCES users(id),
    author_name VARCHAR(255), -- cached for display
    body TEXT NOT NULL,
    mentions JSONB DEFAULT '[]', -- [{"user_id": "...", "name": "..."}]
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_task_comments_task ON task_comments(task_id, created_at);

-- ── Activity trail (UUID-keyed; activity_logs.record_id is BIGINT so the
--    shared chatter tables can't be reused here) ────────────────────────────
CREATE TABLE IF NOT EXISTS task_activity (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    actor_id UUID REFERENCES users(id),
    actor_name VARCHAR(255),
    action VARCHAR(50) NOT NULL, -- created, updated, moved, assigned, unassigned,
                                 -- commented, completed, reopened, archived, ...
    old_value JSONB,
    new_value JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_task_activity_task ON task_activity(task_id, created_at);

-- ── Cross-module links (scaffold: construction_object | crm_deal | contract) ─
CREATE TABLE IF NOT EXISTS task_links (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    linked_module VARCHAR(50) NOT NULL,
    linked_id VARCHAR(100) NOT NULL, -- VARCHAR: construction PKs are INT, CRM PKs are UUID
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (task_id, linked_module, linked_id)
);
CREATE INDEX IF NOT EXISTS idx_task_links_target ON task_links(linked_module, linked_id);

-- ── updated_at triggers (reuse the shared trigger function) ─────────────────
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_task_boards_updated_at') THEN
        CREATE TRIGGER update_task_boards_updated_at BEFORE UPDATE ON task_boards
            FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_task_columns_updated_at') THEN
        CREATE TRIGGER update_task_columns_updated_at BEFORE UPDATE ON task_columns
            FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_tasks_updated_at') THEN
        CREATE TRIGGER update_tasks_updated_at BEFORE UPDATE ON tasks
            FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_task_checklist_items_updated_at') THEN
        CREATE TRIGGER update_task_checklist_items_updated_at BEFORE UPDATE ON task_checklist_items
            FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_task_comments_updated_at') THEN
        CREATE TRIGGER update_task_comments_updated_at BEFORE UPDATE ON task_comments
            FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;
END $$;

-- ── Permissions ─────────────────────────────────────────────────────────────
INSERT INTO permissions (id, module, resource, action, description)
SELECT gen_random_uuid(), 'tasks', r.resource, a.action,
    'Permission to ' || a.action || ' task ' || REPLACE(r.resource, '_', ' ') || 's'
FROM (VALUES ('board'), ('column'), ('task')) AS r(resource)
CROSS JOIN (VALUES ('read'), ('create'), ('update'), ('delete')) AS a(action)
WHERE NOT EXISTS (
    SELECT 1 FROM permissions WHERE module = 'tasks' AND resource = r.resource AND action = a.action
);
