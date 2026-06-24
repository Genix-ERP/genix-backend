-- Per-project task stages (kanban columns). Tasks reference a stage via
-- project_tasks.status = project_task_stages.stage_key.
-- Defaults are seeded lazily by the API the first time a project's stages are
-- listed (see ListProjectStages handler), so no rows are inserted here.

CREATE TABLE IF NOT EXISTS project_task_stages (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    stage_key VARCHAR(100) NOT NULL,
    name VARCHAR(150) NOT NULL,
    color VARCHAR(100),
    position INT NOT NULL DEFAULT 0,
    is_default BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    -- A stage key is unique within a project (tasks match on it)
    UNIQUE(project_id, stage_key)
);

CREATE INDEX IF NOT EXISTS idx_project_task_stages_project ON project_task_stages(project_id);
CREATE INDEX IF NOT EXISTS idx_project_task_stages_tenant ON project_task_stages(tenant_id);
