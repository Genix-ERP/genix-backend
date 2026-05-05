-- ===========================================================
-- One-off data cleanup for a single organization.
--
-- WHAT THIS DOES
-- For one organization id, deletes every row in every table
-- that has an organization_id column matching that id, then
-- (optionally) deletes the organization row itself.
--
-- HOW TO USE
--   1. Take a backup first:
--        docker exec genix-postgres \
--          pg_dump -U yuksalish yuksalisherp \
--          > /tmp/yuksalisherp-pre-wipe-$(date +%F).sql
--   2. Set the target_org variable below to the UUID of the
--      organization you want to wipe.
--   3. Run inside psql:
--        docker exec -i genix-postgres \
--          psql -U yuksalish -d yuksalisherp \
--          < wipe_organization_data.sql
--   4. The script first runs in DRY-RUN mode (BEGIN ... ROLLBACK)
--      and prints how many rows would be deleted from each
--      table. Nothing is actually changed.
--   5. After eyeballing the counts, change `ROLLBACK;` at the
--      bottom to `COMMIT;` and re-run to actually delete.
--
-- SAFETY
--   - Whole thing is wrapped in a single transaction.
--   - Uses information_schema to discover every table with an
--     organization_id column, so we don't miss a table or
--     hard-code stale schema knowledge.
--   - Skips tenant-shared lookup tables that have no
--     organization_id column (chart of accounts, currencies,
--     pipeline types, etc.) — they aren't touched.
--   - Foreign keys without ON DELETE CASCADE will surface as
--     errors during the dry-run, so we'll see them before any
--     real delete.
-- ===========================================================

\set ON_ERROR_STOP on

BEGIN;

-- >>> EDIT THIS LINE: paste the org UUID from the lookup query.
\set target_org '00000000-0000-0000-0000-000000000000'

-- Materialise the target id once so every dynamic statement
-- below picks it up via current_setting().
SELECT set_config('app.target_org', :'target_org', true);

DO $$
DECLARE
    rec        RECORD;
    org_id     UUID := current_setting('app.target_org')::UUID;
    row_count  BIGINT;
    total_rows BIGINT := 0;
BEGIN
    IF org_id = '00000000-0000-0000-0000-000000000000'::uuid THEN
        RAISE EXCEPTION 'target_org has not been set — edit the \set line near the top of this file';
    END IF;

    RAISE NOTICE '-----------------------------------------------------';
    RAISE NOTICE 'Wiping data for organization_id = %', org_id;
    RAISE NOTICE '-----------------------------------------------------';

    FOR rec IN
        SELECT c.table_schema, c.table_name
          FROM information_schema.columns c
          JOIN information_schema.tables  t
            ON t.table_schema = c.table_schema
           AND t.table_name   = c.table_name
         WHERE c.column_name  = 'organization_id'
           AND c.table_schema = 'public'
           AND t.table_type   = 'BASE TABLE'
           AND c.table_name <> 'organizations'   -- handled last
         ORDER BY c.table_name
    LOOP
        EXECUTE format(
            'DELETE FROM %I.%I WHERE organization_id = $1',
            rec.table_schema, rec.table_name
        ) USING org_id;
        GET DIAGNOSTICS row_count = ROW_COUNT;
        total_rows := total_rows + row_count;
        IF row_count > 0 THEN
            RAISE NOTICE '  % rows  ←  %.%',
                lpad(row_count::text, 8), rec.table_schema, rec.table_name;
        END IF;
    END LOOP;

    -- Finally, the organizations row itself.
    DELETE FROM organizations WHERE id = org_id;
    GET DIAGNOSTICS row_count = ROW_COUNT;
    IF row_count > 0 THEN
        RAISE NOTICE '  % rows  ←  organizations  (the company itself)',
            lpad(row_count::text, 8);
        total_rows := total_rows + row_count;
    END IF;

    RAISE NOTICE '-----------------------------------------------------';
    RAISE NOTICE 'TOTAL ROWS THAT WOULD BE DELETED: %', total_rows;
    RAISE NOTICE '-----------------------------------------------------';
    RAISE NOTICE 'This run is a DRY-RUN (ROLLBACK at the bottom).';
    RAISE NOTICE 'Change ROLLBACK to COMMIT and re-run to actually delete.';
END $$;

-- >>> SAFETY: the first run rolls back. After you verify the
-- numbers above look right, change this to COMMIT; and re-run.
ROLLBACK;
-- COMMIT;
