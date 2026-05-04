-- Migration 388: Backfill pipeline_stages.organization_id where NULL.
--
-- ABANDONED for the same reason as migration 387 — the MIN(id) on
-- uuid columns isn't supported by PostgreSQL, and the project owner
-- decided not to backfill historic data.
--
-- The handler patch is enough on its own: every new stage created
-- from now on carries a concrete organization_id (active-org header
-- when present, primary-org fallback otherwise). Pre-existing
-- NULL-org stages remain visible only to admins under the strict
-- list filter; they can be reassigned in the UI on a case-by-case
-- basis if needed.
--
-- Kept as a no-op file so the migration runner records this number
-- as applied and doesn't retry the previously-broken script.

SELECT 1;
