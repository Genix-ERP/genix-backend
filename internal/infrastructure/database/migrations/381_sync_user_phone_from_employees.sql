-- 381_sync_user_phone_from_employees.sql
--
-- Backfills `users.phone` from the linked `employees` record where the
-- two have drifted. Companion to the code-side fix in `UpdateEmployee`
-- and `CreateEmployee` (employee.go) that ADDS reverse mirroring so
-- future edits stay in sync.
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
-- Email backfill intentionally skipped
-- ────────────────────────────────────
-- An earlier draft of this migration also synced `users.email` from
-- `employees.email`, but that runs into the `users_tenant_id_email_key`
-- unique constraint when multiple linked employees within one tenant
-- share an email (which happens in practice). Since email login
-- already works (it matches `users.email` directly and that column was
-- never the broken side), email drift is a contact-info concern only,
-- not a login concern. We leave that for a separate dedup pass.
--
-- What this does
-- ──────────────
-- For every (user, employee) pair linked by `users.employee_id`, copy
-- `employees.phone` into `users.phone` when:
--   * the employee has a non-empty phone
--   * the user's phone is missing OR differs from the employee's
--   * the new phone wouldn't collide with another user's phone in
--     the same tenant (defensive — protects any future
--     unique-on-phone constraint and avoids surprises if two
--     different employees in one tenant somehow share a phone)
--
-- Skips soft-deleted rows on both sides. Idempotent — re-running
-- matches no new rows once the two columns are in sync.

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
  AND COALESCE(u.phone, '') <> e.phone
  AND NOT EXISTS (
      -- Don't overwrite if another live user in this tenant already
      -- has this phone — they'd both end up with the same phone and
      -- phone login would become ambiguous (or hit a future unique
      -- constraint). The remaining drift can be cleaned up by hand.
      SELECT 1
      FROM users u2
      WHERE u2.tenant_id = u.tenant_id
        AND u2.id        <> u.id
        AND u2.phone     = e.phone
        AND u2.deleted_at IS NULL
  );
