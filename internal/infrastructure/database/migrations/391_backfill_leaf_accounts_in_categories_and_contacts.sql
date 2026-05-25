-- 391_backfill_leaf_accounts_in_categories_and_contacts.sql
--
-- Backfill any group/non-leaf account references on product_categories
-- and contacts to a leaf descendant. Why this exists:
--
-- TT §4.2 (migrations 319 + 326) forbids posting to group accounts at
-- the database trigger level. Every JE-emitting handler (CreateBillFromPO,
-- sales invoices, goods receipts, payroll, depreciation, etc.) reads
-- account IDs from product_categories.* and contacts.default_*_account_id
-- to drive its journal-entry-line inserts. If any of those fields point
-- at a group account, the JE trigger rejects the insert and the whole
-- handler 500s — bills can't be created, invoices can't be posted, etc.
--
-- This migration does TWO things:
--
--   1. Fixes the data. For every column that references accounts(id) on
--      product_categories and contacts, replace any value that points at
--      a non-leaf account with the lowest-coded leaf descendant of that
--      group (depth-first, then code ASC). If no leaf descendant exists,
--      NULL the column so the runtime fallback in getCategoryAccounts /
--      getContactDefaultAccount picks a sensible default.
--
--   2. Logs what changed via a NOTICE per row so operators can audit.
--
-- Companion code change in handler/admin_settings.go: findAccount() now
-- filters is_leaf=true at the SQL layer, and resolveLeafAccount() walks
-- the tree at read time as a belt-and-suspenders safety net for any new
-- misconfiguration that slips in after this backfill runs.
--
-- Idempotent: re-running the migration finds zero rows to update on the
-- second pass (everything is already a leaf or NULL).

-- ---------------------------------------------------------------------------
-- Helper: pick the lowest-coded leaf descendant of an account, or NULL.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION mig391_pick_leaf_descendant(p_account_id UUID)
RETURNS UUID AS $$
DECLARE
    v_leaf UUID;
BEGIN
    IF p_account_id IS NULL THEN
        RETURN NULL;
    END IF;
    WITH RECURSIVE descendants AS (
        SELECT id, COALESCE(is_leaf, true) AS leaf, code, 0 AS depth
        FROM accounts
        WHERE parent_id = p_account_id AND deleted_at IS NULL
        UNION ALL
        SELECT a.id, COALESCE(a.is_leaf, true), a.code, d.depth + 1
        FROM accounts a
        JOIN descendants d ON a.parent_id = d.id
        WHERE d.depth < 10 AND a.deleted_at IS NULL
    )
    SELECT id INTO v_leaf
    FROM descendants
    WHERE leaf = true
    ORDER BY depth ASC, code ASC
    LIMIT 1;
    RETURN v_leaf;
END;
$$ LANGUAGE plpgsql;

-- ---------------------------------------------------------------------------
-- Backfill product_categories — five account columns.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    r          RECORD;
    new_id     UUID;
    cols       TEXT[] := ARRAY[
        'income_account_id',
        'expense_account_id',
        'stock_valuation_account_id',
        'stock_input_account_id',
        'stock_output_account_id'
    ];
    col        TEXT;
    cur_id     UUID;
    fixed      INTEGER := 0;
    nulled     INTEGER := 0;
BEGIN
    FOREACH col IN ARRAY cols LOOP
        FOR r IN EXECUTE format(
            'SELECT pc.id AS pc_id, pc.tenant_id, pc.%I AS acct_id, a.code, a.name
             FROM product_categories pc
             JOIN accounts a ON a.id = pc.%I
             WHERE pc.%I IS NOT NULL
               AND COALESCE(a.is_leaf, true) = false', col, col, col)
        LOOP
            cur_id := r.acct_id;
            new_id := mig391_pick_leaf_descendant(cur_id);
            IF new_id IS NOT NULL THEN
                EXECUTE format(
                    'UPDATE product_categories SET %I = $1 WHERE id = $2',
                    col
                ) USING new_id, r.pc_id;
                RAISE NOTICE 'mig391: product_categories.% → fixed pc=% (% %) → leaf=%',
                    col, r.pc_id, r.code, r.name, new_id;
                fixed := fixed + 1;
            ELSE
                EXECUTE format(
                    'UPDATE product_categories SET %I = NULL WHERE id = $1',
                    col
                ) USING r.pc_id;
                RAISE NOTICE 'mig391: product_categories.% → no leaf descendant for pc=% (% %), set NULL',
                    col, r.pc_id, r.code, r.name;
                nulled := nulled + 1;
            END IF;
        END LOOP;
    END LOOP;
    RAISE NOTICE 'mig391: product_categories — fixed=%, nulled=%', fixed, nulled;
END $$;

-- ---------------------------------------------------------------------------
-- Backfill contacts — default_receivable_account_id / default_payable_account_id.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    r       RECORD;
    new_id  UUID;
    cols    TEXT[] := ARRAY['default_receivable_account_id', 'default_payable_account_id'];
    col     TEXT;
    fixed   INTEGER := 0;
    nulled  INTEGER := 0;
BEGIN
    FOREACH col IN ARRAY cols LOOP
        FOR r IN EXECUTE format(
            'SELECT c.id AS contact_id, c.%I AS acct_id, a.code, a.name
             FROM contacts c
             JOIN accounts a ON a.id = c.%I
             WHERE c.%I IS NOT NULL
               AND COALESCE(a.is_leaf, true) = false', col, col, col)
        LOOP
            new_id := mig391_pick_leaf_descendant(r.acct_id);
            IF new_id IS NOT NULL THEN
                EXECUTE format(
                    'UPDATE contacts SET %I = $1 WHERE id = $2', col
                ) USING new_id, r.contact_id;
                RAISE NOTICE 'mig391: contacts.% → fixed contact=% (% %) → leaf=%',
                    col, r.contact_id, r.code, r.name, new_id;
                fixed := fixed + 1;
            ELSE
                EXECUTE format(
                    'UPDATE contacts SET %I = NULL WHERE id = $1', col
                ) USING r.contact_id;
                RAISE NOTICE 'mig391: contacts.% → no leaf descendant for contact=% (% %), set NULL',
                    col, r.contact_id, r.code, r.name;
                nulled := nulled + 1;
            END IF;
        END LOOP;
    END LOOP;
    RAISE NOTICE 'mig391: contacts — fixed=%, nulled=%', fixed, nulled;
END $$;

-- Cleanup: helper function isn't reused anywhere else.
DROP FUNCTION mig391_pick_leaf_descendant(UUID);
