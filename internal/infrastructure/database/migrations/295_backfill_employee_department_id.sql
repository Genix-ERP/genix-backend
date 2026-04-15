-- Backfill department_id from notes JSON for existing employees
-- Resolves department names stored in notes->department to proper department_id FK

UPDATE employees e
SET department_id = d.id
FROM departments d
WHERE e.department_id IS NULL
  AND e.deleted_at IS NULL
  AND e.notes IS NOT NULL
  AND e.notes != ''
  AND e.tenant_id = d.tenant_id
  AND LOWER(TRIM(d.name)) = LOWER(TRIM(e.notes::json->>'department'));
