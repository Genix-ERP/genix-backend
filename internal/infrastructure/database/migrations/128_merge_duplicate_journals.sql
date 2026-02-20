-- Migration: Merge duplicate journals
-- Merges user-created duplicates (SAL, CASH, GEN) into migration-created ones (SALES, CASH_RECEIPTS, GENERAL)
-- For each tenant: reassign journal entries, then soft-delete the duplicate

-- Merge SAL -> SALES (keep SALES)
DO $$
DECLARE
    r RECORD;
BEGIN
    FOR r IN
        SELECT s.id as sal_id, sales.id as sales_id, s.tenant_id
        FROM journals s
        JOIN journals sales ON sales.tenant_id = s.tenant_id AND sales.code = 'SALES' AND sales.deleted_at IS NULL
        WHERE s.code = 'SAL' AND s.deleted_at IS NULL
    LOOP
        -- Reassign journal entries from SAL to SALES
        UPDATE journal_entries SET journal_id = r.sales_id WHERE journal_id = r.sal_id;
        -- Reassign recurring templates
        UPDATE recurring_journal_templates SET journal_id = r.sales_id WHERE journal_id = r.sal_id AND deleted_at IS NULL;
        -- Soft-delete the duplicate
        UPDATE journals SET deleted_at = NOW() WHERE id = r.sal_id;
    END LOOP;
END $$;

-- Merge CASH -> CASH_RECEIPTS (keep CASH_RECEIPTS)
DO $$
DECLARE
    r RECORD;
BEGIN
    FOR r IN
        SELECT c.id as cash_id, cr.id as cr_id, c.tenant_id
        FROM journals c
        JOIN journals cr ON cr.tenant_id = c.tenant_id AND cr.code = 'CASH_RECEIPTS' AND cr.deleted_at IS NULL
        WHERE c.code = 'CASH' AND c.deleted_at IS NULL
    LOOP
        UPDATE journal_entries SET journal_id = r.cr_id WHERE journal_id = r.cash_id;
        UPDATE recurring_journal_templates SET journal_id = r.cr_id WHERE journal_id = r.cash_id AND deleted_at IS NULL;
        UPDATE journals SET deleted_at = NOW() WHERE id = r.cash_id;
    END LOOP;
END $$;

-- Merge GEN -> GENERAL (keep GENERAL)
DO $$
DECLARE
    r RECORD;
BEGIN
    FOR r IN
        SELECT g.id as gen_id, gen.id as general_id, g.tenant_id
        FROM journals g
        JOIN journals gen ON gen.tenant_id = g.tenant_id AND gen.code = 'GENERAL' AND gen.deleted_at IS NULL
        WHERE g.code = 'GEN' AND g.deleted_at IS NULL
    LOOP
        UPDATE journal_entries SET journal_id = r.general_id WHERE journal_id = r.gen_id;
        UPDATE recurring_journal_templates SET journal_id = r.general_id WHERE journal_id = r.gen_id AND deleted_at IS NULL;
        UPDATE journals SET deleted_at = NOW() WHERE id = r.gen_id;
    END LOOP;
END $$;
