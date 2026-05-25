-- Migration 387: Backfill leads.organization_id where NULL.
--
-- ABANDONED. The original SQL used MIN(id) on a uuid column, which
-- PostgreSQL doesn't support (no built-in aggregate min(uuid)), and
-- the production runtime got stuck in a crash loop:
--    "function min(uuid) does not exist"
--
-- Per project owner: don't bother backfilling existing orphan leads.
-- The handler patch (CreateLead/ConvertLead with primary-org
-- fallback) is enough — every new lead going forward carries a
-- concrete organization_id. Any pre-existing NULL-org rows stay as
-- they are; they're visible only to admins (who see the whole
-- tenant) until they're manually reassigned in the UI.
--
-- Kept as a no-op file so the migration runner records this number
-- as applied and doesn't try to re-run a previously-broken script.

SELECT 1;
