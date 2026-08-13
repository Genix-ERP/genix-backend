-- 502_pul_oqimi_unique_guards.sql
--
-- Pul oqimi deep fix (2026-08-13 audit): two uniqueness holes.
--
-- 1. journal_entries numbering for NULL-organization entries. The live unique
--    key is UNIQUE (tenant_id, organization_id, entry_number) (migration 090),
--    and NULLs compare distinct in Postgres — so two concurrent confirms with
--    no org context could both commit e.g. KAS000007 with the constraint never
--    binding. A partial unique index closes exactly that gap. Guarded by a
--    duplicate check: if a deployment already carries NULL-org duplicates the
--    index is skipped (and logged) rather than failing the whole migration —
--    numbering code (entry_number.go) still MAX-scans, so behavior without the
--    index is what it always was.
--
-- 2. cash_orders PKO/RKO numbering race safety depends entirely on a 23505
--    retry against UNIQUE (tenant_id, order_number) (003 / 473 CREATE TABLE).
--    Deployments where 473 found a pre-existing cash_orders table got the
--    columns added but never the constraint — there the MAX+1 race silently
--    duplicates order numbers. Added conditionally, same duplicate guard.

DO $$
BEGIN
    -- No deleted_at filter: the index (like the 090 constraint) spans
    -- soft-deleted rows too — a deleted entry still owns its number.
    IF EXISTS (
        SELECT 1 FROM journal_entries
        WHERE organization_id IS NULL
        GROUP BY tenant_id, entry_number
        HAVING COUNT(*) > 1
        LIMIT 1
    ) THEN
        RAISE NOTICE '502: journal_entries has NULL-org duplicate entry numbers; skipping partial unique index';
    ELSIF NOT EXISTS (
        SELECT 1 FROM pg_indexes
        WHERE tablename = 'journal_entries'
          AND indexname = 'journal_entries_tenant_null_org_entry_number_key'
    ) THEN
        CREATE UNIQUE INDEX journal_entries_tenant_null_org_entry_number_key
            ON journal_entries (tenant_id, entry_number)
            WHERE organization_id IS NULL;
    END IF;
END $$;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'cash_orders'::regclass
          AND contype = 'u'
          AND conname = 'cash_orders_tenant_id_order_number_key'
    ) THEN
        RAISE NOTICE '502: cash_orders unique constraint already present';
    ELSIF EXISTS (
        SELECT 1 FROM cash_orders
        GROUP BY tenant_id, order_number
        HAVING COUNT(*) > 1
        LIMIT 1
    ) THEN
        RAISE NOTICE '502: cash_orders has duplicate order numbers; skipping unique constraint';
    ELSE
        ALTER TABLE cash_orders
            ADD CONSTRAINT cash_orders_tenant_id_order_number_key
            UNIQUE (tenant_id, order_number);
    END IF;
END $$;
