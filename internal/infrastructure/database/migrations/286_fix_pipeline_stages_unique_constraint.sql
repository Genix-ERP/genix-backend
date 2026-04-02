-- Fix unique constraint on pipeline_stages to allow same code across different pipeline types
-- This replaces UNIQUE(tenant_id, code) with UNIQUE(tenant_id, code, pipeline_type)

-- Step 1: Remove duplicates that would block index creation
-- Keep the NEWEST row (most likely has correct data) for each (tenant_id, code, pipeline_type)
DELETE FROM pipeline_stages a
USING pipeline_stages b
WHERE a.ctid < b.ctid
  AND a.tenant_id = b.tenant_id
  AND a.code = b.code
  AND COALESCE(a.pipeline_type, 'opportunity') = COALESCE(b.pipeline_type, 'opportunity');

-- Step 2: Drop ALL unique constraints on pipeline_stages dynamically
DO $$
DECLARE
    r RECORD;
BEGIN
    FOR r IN
        SELECT con.conname
        FROM pg_constraint con
        JOIN pg_class rel ON rel.oid = con.conrelid
        JOIN pg_namespace nsp ON nsp.oid = rel.relnamespace
        WHERE rel.relname = 'pipeline_stages'
          AND con.contype = 'u'
    LOOP
        EXECUTE format('ALTER TABLE pipeline_stages DROP CONSTRAINT IF EXISTS %I', r.conname);
        RAISE NOTICE 'Dropped constraint: %', r.conname;
    END LOOP;
END $$;

-- Step 3: Drop old indexes that might conflict
DROP INDEX IF EXISTS idx_pipeline_stages_unique_code_type;
DROP INDEX IF EXISTS pipeline_stages_tenant_id_code_key;

-- Step 4: Create new unique index scoped by pipeline_type
CREATE UNIQUE INDEX idx_pipeline_stages_unique_code_type
ON pipeline_stages (tenant_id, code, COALESCE(pipeline_type, 'opportunity'));
