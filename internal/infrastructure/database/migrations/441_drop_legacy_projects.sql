-- Migration 441: drop the legacy Loyihalar (projects) tables
--
-- ⚠️ DESTRUCTIVE — runs only after 440 has carried projects/project_tasks/
-- assignees into the new task_* tables. Explicitly approved decision:
-- milestones, time entries, project expenses and team members are removed
-- from the product (budget/timesheets do not belong in task management).
--
-- Notes:
--   - sales_orders keeps its project_id/project_name COLUMNS (data preserved,
--     used by the intercompany flow against construction projects); only the
--     FK to the dropped `projects` table is removed.
--   - purchase_invoices rows created from project expenses remain untouched —
--     they are real payables; only the back-link table goes away.
--   - The old 'projects' permission rows are removed; role_permissions rows
--     cascade via their FK.

ALTER TABLE sales_orders DROP CONSTRAINT IF EXISTS sales_orders_project_id_fkey;

DROP TABLE IF EXISTS time_entries CASCADE;
DROP TABLE IF EXISTS project_expenses CASCADE;
DROP TABLE IF EXISTS project_team_members CASCADE;
DROP TABLE IF EXISTS project_milestones CASCADE;
DROP TABLE IF EXISTS project_tasks CASCADE;
DROP TABLE IF EXISTS projects CASCADE;

DELETE FROM permissions WHERE module = 'projects';
