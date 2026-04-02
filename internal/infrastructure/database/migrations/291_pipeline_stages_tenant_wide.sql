-- Pipeline stages should be tenant-wide, not per-organization.
-- Multiple orgs seeded duplicate stages with the same code — deduplicate and make tenant-wide.

-- Step 1: Drop the org-scoped unique index so we can deduplicate
DROP INDEX IF EXISTS idx_pipeline_stages_unique_code_type_org;

-- Step 2: Remove duplicates — keep the stage with is_won/is_lost set correctly (highest ctid wins)
DELETE FROM pipeline_stages a
USING pipeline_stages b
WHERE a.ctid < b.ctid
  AND a.tenant_id = b.tenant_id
  AND a.code = b.code
  AND COALESCE(a.pipeline_type, 'opportunity') = COALESCE(b.pipeline_type, 'opportunity');

-- Step 3: Clear organization_id so all employees in same tenant see the same stages
UPDATE pipeline_stages SET organization_id = NULL WHERE organization_id IS NOT NULL;

-- Step 4: Recreate unique index without organization_id scope
CREATE UNIQUE INDEX idx_pipeline_stages_unique_code_type_org
ON pipeline_stages (tenant_id, code, COALESCE(pipeline_type, 'opportunity'));
