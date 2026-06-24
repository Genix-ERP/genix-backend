-- Multiple assignees per task. The legacy project_tasks.assignee_id/assignee_name
-- columns are kept (primary assignee) for backward compatibility.
CREATE TABLE IF NOT EXISTS project_task_assignees (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    task_id UUID NOT NULL REFERENCES project_tasks(id) ON DELETE CASCADE,
    employee_id UUID NOT NULL,
    employee_name VARCHAR(255),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(task_id, employee_id)
);
CREATE INDEX IF NOT EXISTS idx_task_assignees_task ON project_task_assignees(task_id);
CREATE INDEX IF NOT EXISTS idx_task_assignees_tenant ON project_task_assignees(tenant_id);

-- Backfill from the existing single assignee
INSERT INTO project_task_assignees (id, tenant_id, task_id, employee_id, employee_name)
SELECT uuid_generate_v4(), tenant_id, id, assignee_id, assignee_name
FROM project_tasks
WHERE assignee_id IS NOT NULL
ON CONFLICT (task_id, employee_id) DO NOTHING;
