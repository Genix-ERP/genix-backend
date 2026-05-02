-- 379_optimize_sub_derived_trigger.sql
--
-- Converts migration 375's sub_derived trigger from per-row to per-
-- statement using transition tables.
--
-- Why this matters
-- ────────────────
-- The estimate import flow does TWO bulk operations on
-- construction_estimate_line in sequence:
--
--   1. INSERT 3000+ child rows (parent_line_id = NULL initially —
--      375's trigger short-circuits here, so this is fine).
--   2. UPDATE every child to set its newly-resolved parent_line_id +
--      subline_seq (BulkCreateEstimateLines, around line 1608 in
--      construction_estimate.go). For 3000 rows the per-row 375
--      trigger fires 3000 times, each running SELECT SUM + UPDATE
--      on the parent. The same parent gets refreshed dozens of times
--      redundantly (once per child it gained), and on real-world
--      tenants the entire post-import phase took 30-60 s.
--
-- Statement-level triggers fire once per SQL statement and use
-- "transition tables" (NEW TABLE / OLD TABLE) to see all the rows
-- the statement touched. We then issue ONE bulk UPDATE that refreshes
-- sub_derived for the DISTINCT set of affected parents — turning
-- O(N children) trigger work into O(parent_count) work.
--
-- Postgres 10+ required (REFERENCING ... TABLE syntax). All current
-- target environments are on 14+, so this is safe.
--
-- The per-row helper `construction_estimate_line_refresh_sub_derived`
-- from 375 is kept around in case ad-hoc admin scripts call it; it's
-- harmless either way.

-- Drop the per-row trigger from 375.
DROP TRIGGER IF EXISTS trg_construction_estimate_line_sub_derived
    ON construction_estimate_line;

-- ──────────────────────────────────────────────────────────────────
-- AFTER INSERT — refresh parents that gained new children.
-- Reads `new_rows` transition table.
-- ──────────────────────────────────────────────────────────────────
CREATE OR REPLACE FUNCTION construction_estimate_line_sub_derived_after_insert()
RETURNS TRIGGER AS $$
BEGIN
    UPDATE construction_estimate_line p
    SET sub_derived = COALESCE((
        SELECT SUM(COALESCE(c.unit_rate, 0) * COALESCE(c.norm_rate, 0))
        FROM construction_estimate_line c
        WHERE c.parent_line_id = p.id
          AND c.tenant_id = p.tenant_id
          AND COALESCE(c.resource_type, '') <> ''
    ), 0)
    FROM (
        SELECT DISTINCT parent_line_id, tenant_id
        FROM new_rows
        WHERE parent_line_id IS NOT NULL
    ) ap
    WHERE p.id = ap.parent_line_id
      AND p.tenant_id = ap.tenant_id;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

-- ──────────────────────────────────────────────────────────────────
-- AFTER DELETE — refresh parents that lost children.
-- ──────────────────────────────────────────────────────────────────
CREATE OR REPLACE FUNCTION construction_estimate_line_sub_derived_after_delete()
RETURNS TRIGGER AS $$
BEGIN
    UPDATE construction_estimate_line p
    SET sub_derived = COALESCE((
        SELECT SUM(COALESCE(c.unit_rate, 0) * COALESCE(c.norm_rate, 0))
        FROM construction_estimate_line c
        WHERE c.parent_line_id = p.id
          AND c.tenant_id = p.tenant_id
          AND COALESCE(c.resource_type, '') <> ''
    ), 0)
    FROM (
        SELECT DISTINCT parent_line_id, tenant_id
        FROM old_rows
        WHERE parent_line_id IS NOT NULL
    ) ap
    WHERE p.id = ap.parent_line_id
      AND p.tenant_id = ap.tenant_id;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

-- ──────────────────────────────────────────────────────────────────
-- AFTER UPDATE — refresh BOTH the OLD parent (if a row was re-parented
-- away) and the NEW parent (if a row gained / kept a parent). The
-- DISTINCT UNION dedups so each parent is refreshed at most once even
-- when many rows of the same statement touch it.
-- ──────────────────────────────────────────────────────────────────
CREATE OR REPLACE FUNCTION construction_estimate_line_sub_derived_after_update()
RETURNS TRIGGER AS $$
BEGIN
    UPDATE construction_estimate_line p
    SET sub_derived = COALESCE((
        SELECT SUM(COALESCE(c.unit_rate, 0) * COALESCE(c.norm_rate, 0))
        FROM construction_estimate_line c
        WHERE c.parent_line_id = p.id
          AND c.tenant_id = p.tenant_id
          AND COALESCE(c.resource_type, '') <> ''
    ), 0)
    FROM (
        SELECT DISTINCT parent_line_id, tenant_id FROM new_rows WHERE parent_line_id IS NOT NULL
        UNION
        SELECT DISTINCT parent_line_id, tenant_id FROM old_rows WHERE parent_line_id IS NOT NULL
    ) ap
    WHERE p.id = ap.parent_line_id
      AND p.tenant_id = ap.tenant_id;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

-- ──────────────────────────────────────────────────────────────────
-- Triggers — three separate ones because transition tables are scoped
-- to the operation that produced them (NEW TABLE for INSERT/UPDATE,
-- OLD TABLE for DELETE/UPDATE). They all fire once per statement.
-- ──────────────────────────────────────────────────────────────────
DROP TRIGGER IF EXISTS trg_cel_sub_derived_insert ON construction_estimate_line;
DROP TRIGGER IF EXISTS trg_cel_sub_derived_update ON construction_estimate_line;
DROP TRIGGER IF EXISTS trg_cel_sub_derived_delete ON construction_estimate_line;

CREATE TRIGGER trg_cel_sub_derived_insert
    AFTER INSERT ON construction_estimate_line
    REFERENCING NEW TABLE AS new_rows
    FOR EACH STATEMENT
    EXECUTE FUNCTION construction_estimate_line_sub_derived_after_insert();

CREATE TRIGGER trg_cel_sub_derived_update
    AFTER UPDATE ON construction_estimate_line
    REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows
    FOR EACH STATEMENT
    EXECUTE FUNCTION construction_estimate_line_sub_derived_after_update();

CREATE TRIGGER trg_cel_sub_derived_delete
    AFTER DELETE ON construction_estimate_line
    REFERENCING OLD TABLE AS old_rows
    FOR EACH STATEMENT
    EXECUTE FUNCTION construction_estimate_line_sub_derived_after_delete();
