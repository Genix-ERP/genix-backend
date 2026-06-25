-- Dedup flags so the workflow scheduler fires "overdue task" and
-- "over-budget project" notifications only once per condition, instead of
-- every 15-minute tick.

ALTER TABLE project_tasks ADD COLUMN IF NOT EXISTS overdue_notified BOOLEAN DEFAULT false;
ALTER TABLE projects ADD COLUMN IF NOT EXISTS over_budget_notified BOOLEAN DEFAULT false;

-- When a task moves back out of an overdue state (completed/cancelled or the
-- due date is pushed out) the flag is reset by the scheduler so a future
-- breach notifies again. Index keeps the scheduler scan cheap.
CREATE INDEX IF NOT EXISTS idx_project_tasks_overdue_scan
    ON project_tasks (tenant_id, due_date)
    WHERE deleted_at IS NULL;
