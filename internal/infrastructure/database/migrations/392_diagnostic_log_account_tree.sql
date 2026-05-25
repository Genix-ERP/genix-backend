-- 392_diagnostic_log_account_tree.sql
--
-- Diagnostic migration. Logs (via NOTICE) the children of every group
-- account under section 1xxx, 4xxx, 6xxx and 9xxx — the four sections
-- that drive Stock Input, AR, AP, and COGS lookups in CreateBillFromPO,
-- sales invoices, payroll, etc.
--
-- Output goes to the migration log when this is applied; share with the
-- engineer who wrote findAccount() if a JE-emitting handler keeps
-- failing TT §4.2 even after migration 391 ran.
--
-- Idempotent and read-only — does not modify any data.

DO $$
DECLARE
    grp RECORD;
    leaf_count INTEGER;
BEGIN
    FOR grp IN
        SELECT a.id, a.tenant_id, a.organization_id, a.code, a.name, a.is_leaf
        FROM accounts a
        WHERE a.deleted_at IS NULL
          AND a.code ~ '^[1469][0-9]{2,3}$'
          AND COALESCE(a.is_leaf, true) = false
        ORDER BY a.organization_id, a.code
    LOOP
        SELECT COUNT(*) INTO leaf_count
        FROM accounts c
        WHERE c.parent_id = grp.id
          AND c.deleted_at IS NULL
          AND COALESCE(c.is_leaf, true) = true;

        IF leaf_count = 0 THEN
            RAISE WARNING 'mig392: account % (% / %) is is_leaf=false but has 0 leaf children — postings will fail TT §4.2',
                grp.code, grp.name, grp.id;
        ELSE
            RAISE NOTICE 'mig392: account % (% / %) has % leaf children',
                grp.code, grp.name, grp.id, leaf_count;
        END IF;
    END LOOP;
END $$;
