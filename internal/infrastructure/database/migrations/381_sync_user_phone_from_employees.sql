-- 381_sync_user_phone_from_employees.sql
--
-- Backfills `users.phone` (and `users.email`) from the linked
-- `employees` record where the two have drifted. Companion to the
-- code-side fix in `UpdateEmployee` and `CreateEmployee` (employee.go)
-- that ADDS reverse mirroring so future edits stay in sync.
--
-- Why this is needed
-- ──────────────────
-- Login matches `users.phone`, but HR-side phone updates only ever
-- wrote `employees.phone`. The reverse mirror was missing, so users
-- gradually accumulated stale `users.phone` values. The reported
-- symptom was a user (`bsotuv@gmail.com`) whose phone-login failed
-- with 401 even though email-login worked — `users.phone` held the
-- old OTP-registration number while `employees.phone` held the
-- current canonical work number. With this backfill, login
-- consistently sees the same phone HR sees on the employee record.
--
-- What this does
-- ──────────────
--   1. For every (user, employee) pair linked by `users.employee_id`,
--      copy `employees.phone` into `users.phone` when:
--        - the employee has a non-empty phone, AND
--        - the user's phone is missing OR differs from the employee's.
--
--   2. Same for email.
--
--   3. Skips soft-deleted rows on both sides.
--
-- The change is idempotent — re-running matches no new rows once the
-- two columns are in sync. Wrapped in a single UPDATE so it's atomic.

-- Phone backfill
UPDATE users u
SET phone      = e.phone,
    updated_at = NOW()
FROM employees e
WHERE u.employee_id = e.id
  AND u.tenant_id   = e.tenant_id
  AND u.deleted_at IS NULL
  AND e.deleted_at IS NULL
  AND e.phone IS NOT NULL
  AND e.phone <> ''
  AND COALESCE(u.phone, '') <> e.phone;

-- Email backfill (same drift pattern, same one-way sync gap)
UPDATE users u
SET email      = e.email,
    updated_at = NOW()
FROM employees e
WHERE u.employee_id = e.id
  AND u.tenant_id   = e.tenant_id
  AND u.deleted_at IS NULL
  AND e.deleted_at IS NULL
  AND e.email IS NOT NULL
  AND e.email <> ''
  AND COALESCE(u.email, '') <> e.email;
