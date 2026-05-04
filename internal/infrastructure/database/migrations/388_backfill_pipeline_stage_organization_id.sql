-- Migration 388: Backfill pipeline_stages.organization_id where NULL.
--
-- Same problem as migration 387 (leads), now for pipeline_stages.
-- Stages created before per-org scoping was introduced — and stages
-- created later by an admin without an active company — were stored
-- with organization_id = NULL. The handler used to allow listing
-- those NULL rows for any org viewer (an "OR organization_id IS NULL"
-- branch in the WHERE clause), but reordering them updated only the
-- admin's view because the broad filter masked the underlying scope
-- mismatch. The handler is now strictly org-scoped on list and
-- update, and CreatePipelineStage falls back to the creator's
-- primary org when no header is present. This migration cleans up
-- the historic NULL rows so the strict filter doesn't suddenly hide
-- a tenant's entire pipeline.
--
-- Strategy:
--   1. For single-org tenants, attach every NULL-org stage to that
--      tenant's only org (unambiguous).
--   2. For multi-org tenants, fan the NULL-org stages out to every
--      organization in the tenant by inserting per-org copies — so
--      no company loses a stage someone was previously using.
--      The original NULL row is then deleted.
--   3. Anything still NULL after that should not exist; left alone
--      as belt-and-braces.

-- Step 1: single-org tenants — direct attach.
UPDATE pipeline_stages ps
   SET organization_id = o.id,
       updated_at      = NOW()
  FROM (
        SELECT tenant_id, MIN(id) AS id
          FROM organizations
         WHERE deleted_at IS NULL
         GROUP BY tenant_id
        HAVING COUNT(*) = 1
       ) o
 WHERE ps.organization_id IS NULL
   AND ps.tenant_id = o.tenant_id;

-- Step 2: multi-org tenants — fan out copies, then delete originals.
-- Use a CTE to materialise the list of (null-stage, target-org) pairs
-- before INSERTing, otherwise the INSERT … SELECT would race with the
-- DELETE on the same rows.
WITH null_stages AS (
    SELECT * FROM pipeline_stages WHERE organization_id IS NULL
),
targets AS (
    SELECT ns.id AS source_id,
           ns.tenant_id, ns.name, ns.custom_name, ns.code, ns.sequence,
           ns.probability, ns.is_won, ns.is_lost, ns.color, ns.is_active,
           ns.pipeline_type,
           o.id AS target_org_id
      FROM null_stages ns
      JOIN organizations o
        ON o.tenant_id = ns.tenant_id
       AND o.deleted_at IS NULL
)
INSERT INTO pipeline_stages (
    id, tenant_id, name, custom_name, code, sequence, probability,
    is_won, is_lost, color, is_active, pipeline_type, organization_id,
    created_at, updated_at
)
SELECT uuid_generate_v4(), tenant_id, name, custom_name, code, sequence,
       probability, is_won, is_lost, color, is_active, pipeline_type,
       target_org_id, NOW(), NOW()
  FROM targets
ON CONFLICT DO NOTHING;

-- Step 2b: drop the original NULL rows now that copies exist per org.
-- Only drop if at least one org-scoped copy was created for them
-- (defensive: avoid losing data on a tenant that has zero orgs).
DELETE FROM pipeline_stages ns
 WHERE ns.organization_id IS NULL
   AND EXISTS (
        SELECT 1 FROM organizations o
         WHERE o.tenant_id = ns.tenant_id
           AND o.deleted_at IS NULL
   );
