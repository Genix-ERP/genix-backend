-- Add data-changing actions to two project workflow rules (existing tenants):
--   * Overdue task rule  -> also raise the task's priority to high
--   * Milestone completed -> also create a follow-up task in the project
-- These are appended to the existing actions array (which keeps the
-- notification). NOT LIKE guards make the migration safe/idempotent.

UPDATE workflow_rules
SET actions = actions || '[{"type":"update_task_priority","config":{"priority":"high"}}]'::jsonb,
    updated_at = NOW()
WHERE trigger_event = 'project.task.overdue'
  AND category = 'project'
  AND deleted_at IS NULL
  AND actions::text NOT LIKE '%update_task_priority%';

UPDATE workflow_rules
SET actions = actions || '[{"type":"create_followup_task","config":{"title":"{milestone_title} — keyingi qadamlar"}}]'::jsonb,
    updated_at = NOW()
WHERE trigger_event = 'project.milestone.completed'
  AND category = 'project'
  AND deleted_at IS NULL
  AND actions::text NOT LIKE '%create_followup_task%';
