-- Migration 440: Loyihalar → Vazifalar data carry-over
--
-- Why needed:
--   Migration 439 created the new task-management schema. Existing Loyihalar
--   data must move into it before 441 drops the legacy tables.
--
-- What it does:
--   1. Every live (non-soft-deleted) `projects` row becomes a task_board,
--      REUSING the project id as the board id (traceable + idempotent).
--      Completed/cancelled projects arrive archived.
--   2. Every board gets the 4 default columns:
--      Yangi → Jarayonda → Tekshiruvda → Bajarildi (done column).
--   3. Every live `project_tasks` row becomes a task (reusing its id), placed
--      into the column matching its old status; completed tasks get
--      completed_at, cancelled tasks arrive archived. Position = creation order.
--   4. Single assignee_id fans out into task_assignees.
--   5. Access carry-over: employee_module_permissions 'projects' → 'tasks',
--      roles.permissions JSONB key 'projects' → 'tasks',
--      tenant_installed_apps app 'projects' → 'tasks'.
--
-- What is intentionally skipped:
--   - Soft-deleted projects/tasks (user-deleted trash).
--   - project_milestones / time_entries / project_expenses /
--     project_team_members — out of scope for task management (budget and
--     timesheets do not belong here); dropped in 441 per explicit decision.
--
-- Idempotent — re-running matches no new rows (ON CONFLICT / NOT EXISTS guards).

-- 1. Boards from projects (board id = old project id)
INSERT INTO task_boards (id, tenant_id, organization_id, name, description, color,
                         created_by, archived_at, created_at, updated_at)
SELECT p.id, p.tenant_id, p.organization_id, p.project_name, NULLIF(p.description, ''),
       'blue', p.created_by,
       CASE WHEN p.status IN ('completed', 'cancelled') THEN CURRENT_TIMESTAMP END,
       p.created_at, CURRENT_TIMESTAMP
FROM projects p
WHERE p.deleted_at IS NULL
ON CONFLICT (id) DO NOTHING;

-- 2. Default columns for carried-over boards that have none yet
INSERT INTO task_columns (tenant_id, board_id, name, color, position, is_done_column)
SELECT b.tenant_id, b.id, c.name, c.color, c.pos, c.is_done
FROM task_boards b
CROSS JOIN (VALUES
    ('Yangi',       'gray',   0, false),
    ('Jarayonda',   'blue',   1, false),
    ('Tekshiruvda', 'orange', 2, false),
    ('Bajarildi',   'green',  3, true)
) AS c(name, color, pos, is_done)
WHERE NOT EXISTS (SELECT 1 FROM task_columns tc WHERE tc.board_id = b.id);

-- 3. Tasks from project_tasks (task id = old task id)
INSERT INTO tasks (id, tenant_id, board_id, column_id, title, description, priority,
                   due_date, position, completed_at, archived_at, created_by,
                   created_at, updated_at)
SELECT pt.id, pt.tenant_id, pt.project_id, tc.id, pt.title, NULLIF(pt.description, ''),
       CASE pt.priority
           WHEN 'low' THEN 'low'
           WHEN 'high' THEN 'high'
           WHEN 'urgent' THEN 'urgent'
           WHEN 'critical' THEN 'urgent'
           ELSE 'normal'
       END,
       pt.due_date,
       ROW_NUMBER() OVER (PARTITION BY pt.project_id, tc.id ORDER BY pt.created_at) - 1,
       CASE WHEN pt.status = 'completed' THEN pt.updated_at END,
       CASE WHEN pt.status = 'cancelled' THEN CURRENT_TIMESTAMP END,
       pt.created_by, pt.created_at, pt.updated_at
FROM project_tasks pt
JOIN task_boards b ON b.id = pt.project_id
JOIN task_columns tc ON tc.board_id = b.id
    AND tc.name = CASE pt.status
        WHEN 'in_progress' THEN 'Jarayonda'
        WHEN 'review'      THEN 'Tekshiruvda'
        WHEN 'completed'   THEN 'Bajarildi'
        ELSE 'Yangi'
    END
WHERE pt.deleted_at IS NULL
ON CONFLICT (id) DO NOTHING;

-- 4. Assignees (only employees that still exist)
INSERT INTO task_assignees (task_id, employee_id, tenant_id, created_at)
SELECT t.id, pt.assignee_id, t.tenant_id, t.created_at
FROM tasks t
JOIN project_tasks pt ON pt.id = t.id
JOIN employees e ON e.id = pt.assignee_id
WHERE pt.assignee_id IS NOT NULL
ON CONFLICT (task_id, employee_id) DO NOTHING;

-- 5a. Module-level employee permissions: projects → tasks
UPDATE employee_module_permissions emp
SET module_id = 'tasks'
WHERE emp.module_id = 'projects'
  AND NOT EXISTS (
      SELECT 1 FROM employee_module_permissions e2
      WHERE e2.tenant_id = emp.tenant_id
        AND e2.employee_id = emp.employee_id
        AND e2.module_id = 'tasks'
  );

-- 5b. Tenant role JSONB blobs: key 'projects' → 'tasks'
UPDATE roles
SET permissions = (permissions - 'projects') || jsonb_build_object('tasks', permissions -> 'projects')
WHERE permissions ? 'projects'
  AND NOT (permissions ? 'tasks');

-- 5c. Installed apps: projects → tasks
UPDATE tenant_installed_apps tia
SET app_id = 'tasks', app_name = 'Vazifalar'
WHERE tia.app_id = 'projects'
  AND NOT EXISTS (
      SELECT 1 FROM tenant_installed_apps t2
      WHERE t2.tenant_id = tia.tenant_id AND t2.app_id = 'tasks'
  );
