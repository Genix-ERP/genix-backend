-- Fix unique constraint on pipeline_stages to allow same code across different pipeline types and organizations
-- Old constraint: UNIQUE(tenant_id, code) prevents lead and opportunity stages from sharing codes
-- New constraint: UNIQUE(tenant_id, code, pipeline_type) scoped per pipeline type

-- Step 1: Remove duplicates that would block index creation
-- Keep the oldest row for each (tenant_id, code, pipeline_type) combo
DELETE FROM pipeline_stages a
USING pipeline_stages b
WHERE a.ctid < b.ctid
  AND a.tenant_id = b.tenant_id
  AND a.code = b.code
  AND COALESCE(a.pipeline_type, 'opportunity') = COALESCE(b.pipeline_type, 'opportunity');

-- Step 2: Drop the old unique constraint (try multiple possible names)
DO $$
DECLARE
    r RECORD;
BEGIN
    -- Find and drop ALL unique constraints/indexes on (tenant_id, code) for pipeline_stages
    FOR r IN
        SELECT con.conname
        FROM pg_constraint con
        JOIN pg_class rel ON rel.oid = con.conrelid
        WHERE rel.relname = 'pipeline_stages'
          AND con.contype = 'u'
          AND array_length(con.conkey, 1) = 2
    LOOP
        EXECUTE format('ALTER TABLE pipeline_stages DROP CONSTRAINT IF EXISTS %I', r.conname);
    END LOOP;
END $$;

-- Also try the common auto-generated name directly
ALTER TABLE pipeline_stages DROP CONSTRAINT IF EXISTS pipeline_stages_tenant_id_code_key;

-- Step 3: Drop old index if it exists
DROP INDEX IF EXISTS idx_pipeline_stages_unique_code_type;

-- Step 4: Create new unique index scoped by pipeline_type
CREATE UNIQUE INDEX idx_pipeline_stages_unique_code_type
ON pipeline_stages (tenant_id, code, COALESCE(pipeline_type, 'opportunity'));
