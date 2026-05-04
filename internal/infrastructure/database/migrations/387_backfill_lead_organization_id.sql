-- Migration 387: Backfill leads.organization_id where it is NULL.
--
-- Bug: Leads created via the CRM by an admin (or anyone whose
-- request didn't carry an X-Organization-ID header) were stored
-- with organization_id = NULL. ListLeads filters strictly by org,
-- so those leads became visible only to admins (no org filter)
-- and the creator — never to teammates in the same company.
-- The handler is now patched to always populate organization_id
-- via a primary-org fallback. This migration cleans up the
-- existing NULL rows so they actually surface to their company.
--
-- Strategy:
--   1. Try the creator's primary employee_organizations entry
--      (is_primary=true wins, then earliest).
--   2. If the creator has no employee record, fall back to the
--      tenant's first organization (single-org tenants make this
--      unambiguous; multi-org tenants will hit the rule above
--      first because admins are normally employees).
--   3. Anything still NULL is left alone so a human can decide.

-- Step 1: creator-based mapping
UPDATE leads l
   SET organization_id = sub.org_id,
       updated_at      = NOW()
  FROM (
        SELECT DISTINCT ON (e.user_id, e.tenant_id)
               e.user_id,
               e.tenant_id,
               eo.organization_id AS org_id
          FROM employees e
          JOIN employee_organizations eo
            ON eo.employee_id = e.id
           AND eo.tenant_id   = e.tenant_id
         WHERE e.deleted_at IS NULL
         ORDER BY e.user_id, e.tenant_id,
                  eo.is_primary DESC,
                  eo.created_at ASC
       ) sub
 WHERE l.organization_id IS NULL
   AND l.created_by IS NOT NULL
   AND l.created_by = sub.user_id
   AND l.tenant_id  = sub.tenant_id;

-- Step 2: tenant-with-single-org fallback for the rest
UPDATE leads l
   SET organization_id = o.id,
       updated_at      = NOW()
  FROM (
        SELECT tenant_id, MIN(id) AS id
          FROM organizations
         WHERE deleted_at IS NULL
         GROUP BY tenant_id
        HAVING COUNT(*) = 1
       ) o
 WHERE l.organization_id IS NULL
   AND l.tenant_id = o.tenant_id;

-- Anything still NULL after the above belongs to a multi-org
-- tenant where the creator has no employee record. Leave for
-- manual reassignment rather than guessing.
