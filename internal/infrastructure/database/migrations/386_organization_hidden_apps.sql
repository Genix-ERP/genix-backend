-- 386_organization_hidden_apps.sql
--
-- Per-organization "hide app" override.
--
-- Background: app installation is tenant-level (tenant_installed_apps).
-- That works for single-org tenants but breaks for groups with mixed
-- subsidiaries — e.g. Yuksalish Group has a Cargo subsidiary that
-- doesn't use Construction. Uninstalling Construction from the tenant
-- would hide it everywhere; we don't want that.
--
-- This migration adds a per-org denylist: `hidden_apps text[]`. When
-- a user is operating inside an org whose `hidden_apps` includes the
-- given app_id, the sidebar entry is hidden. The backend route is NOT
-- gated — typing the URL still loads the page. Hiding is purely a
-- sidebar/visibility concern.
--
-- Behaviour spec (decided with user):
--   * Empty array (the default) ⇒ identical to existing behaviour, app
--     visible everywhere it's installed tenant-wide.
--   * On tenant-wide UNINSTALL, the app_id is scrubbed from every
--     organization's hidden_apps so a fresh reinstall starts clean
--     (decided: "we would not keep").
--   * Backend APIs remain accessible regardless — the user explicitly
--     accepted this ("if user types construction in url, it is not
--     danger or risk").
--
-- Idempotent: ADD COLUMN IF NOT EXISTS.

ALTER TABLE organizations
    ADD COLUMN IF NOT EXISTS hidden_apps TEXT[] NOT NULL DEFAULT '{}';

COMMENT ON COLUMN organizations.hidden_apps IS
    'Per-org sidebar visibility override. If app_id ∈ hidden_apps, the '
    'app does NOT appear in the sidebar when this org is the active '
    'company. Cleared on tenant-wide uninstall (see UninstallApp handler).';
