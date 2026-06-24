-- Link a task to a milestone (bosqich). Nullable: tasks may be unassigned.
ALTER TABLE project_tasks ADD COLUMN IF NOT EXISTS milestone_id UUID REFERENCES project_milestones(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_project_tasks_milestone ON project_tasks(milestone_id);
