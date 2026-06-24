-- Notes / comments left on a project task. Each note records who wrote it and when.
CREATE TABLE IF NOT EXISTS project_task_notes (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    task_id UUID NOT NULL REFERENCES project_tasks(id) ON DELETE CASCADE,
    note TEXT NOT NULL,
    created_by UUID,
    created_by_name VARCHAR(255),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_project_task_notes_task ON project_task_notes(task_id);
CREATE INDEX IF NOT EXISTS idx_project_task_notes_tenant ON project_task_notes(tenant_id);
