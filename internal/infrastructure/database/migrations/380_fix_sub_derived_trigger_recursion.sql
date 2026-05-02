-- 380_fix_sub_derived_trigger_recursion.sql
--
-- Fixes the "stack depth limit exceeded" error introduced by migration
-- 379's statement-level sub_derived triggers. Symptom:
--
--   pq: stack depth limit exceeded
--   on every INSERT into construction_estimate_line
--
-- Root cause
-- ──────────
-- Statement-level triggers fire AFTER the statement runs *regardless
-- of whether any rows were actually modified*. Migration 379's trigger
-- functions did:
--
--   UPDATE construction_estimate_line p
--   SET sub_derived = …
--   FROM (SELECT … FROM new_rows WHERE parent_line_id IS NOT NULL) ap
--   WHERE p.id = ap.parent_line_id …;
--
-- When `new_rows` had no rows with parent_line_id IS NOT NULL (e.g.
-- the bulk-import INSERT writes children with parent_line_id = NULL),
-- the subquery yielded 0 rows, the UPDATE matched 0 rows — but Postgres
-- STILL fired the AFTER UPDATE trigger because the UPDATE statement
-- ran. That UPDATE trigger ran another UPDATE (also 0 rows), which
-- fired the trigger again, which ran another UPDATE, etc. Recursion
-- terminated only when Postgres ran out of stack (~1000 levels).
--
-- Fix
-- ───
-- Each trigger function now does an early-return when there's nothing
-- to do — checks the transition table for at least one row with a
-- non-NULL parent_line_id, and returns immediately if none. The actual
-- UPDATE only runs when there's real work, so subsequent trigger
-- fires either find more work (legitimate parent-chain refresh, depth
-- bounded by the parent_line_id hierarchy — typically 1-2 levels) or
-- short-circuit cleanly.

CREATE OR REPLACE FUNCTION construction_estimate_line_sub_derived_after_insert()
RETURNS TRIGGER AS $$
BEGIN
    -- Early-return when no inserted row has a parent — avoids the
    -- 0-row UPDATE that would otherwise re-fire this trigger via the
    -- statement-level mechanism.
    IF NOT EXISTS (
        SELECT 1 FROM new_rows WHERE parent_line_id IS NOT NULL
    ) THEN
        RETURN NULL;
    END IF;

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

CREATE OR REPLACE FUNCTION construction_estimate_line_sub_derived_after_delete()
RETURNS TRIGGER AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM old_rows WHERE parent_line_id IS NOT NULL
    ) THEN
        RETURN NULL;
    END IF;

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

CREATE OR REPLACE FUNCTION construction_estimate_line_sub_derived_after_update()
RETURNS TRIGGER AS $$
BEGIN
    -- Both transition tables must lack a non-NULL parent_line_id for
    -- us to bail out — otherwise we still need to refresh the
    -- corresponding parent.
    IF NOT EXISTS (
        SELECT 1 FROM new_rows WHERE parent_line_id IS NOT NULL
    ) AND NOT EXISTS (
        SELECT 1 FROM old_rows WHERE parent_line_id IS NOT NULL
    ) THEN
        RETURN NULL;
    END IF;

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
