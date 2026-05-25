-- 332_estimate_sublines.sql
-- Adds first-class sub-line (подкатор) support to construction_estimate_line.
--
-- Background:
--   The existing schema already has a string-based `parent_item_number` column
--   (migration 226). This migration hardens that into a real FK (`parent_line_id`)
--   and adds `norm_rate` for the ШРНК норма that drives the sub-line's effective
--   quantity. `subline_seq` speeds up auto-numbering of the next child
--   ("32-N" where N = max(subline_seq)+1).
--
-- Ordering matters: backfill BEFORE creating the unique index, otherwise legacy
-- rows with duplicate subline_seq = 0 (the column default) collide with each
-- other per parent. An earlier version of this migration failed in exactly
-- that spot — the fix is to finish backfilling both parent_line_id and
-- subline_seq, then add the unique index at the end.

-- ─────────────────────────────────────────────────────────────────────────────
-- 1) New columns (no indexes yet)
-- ─────────────────────────────────────────────────────────────────────────────
ALTER TABLE construction_estimate_line
    ADD COLUMN IF NOT EXISTS parent_line_id BIGINT
        REFERENCES construction_estimate_line(id) ON DELETE CASCADE,
    ADD COLUMN IF NOT EXISTS norm_rate   DECIMAL(18,6) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS subline_seq INTEGER       NOT NULL DEFAULT 0;

-- ─────────────────────────────────────────────────────────────────────────────
-- 2) Backfill parent_line_id from the legacy parent_item_number string.
--    This lets any rows created before this migration keep working without
--    forcing the admin to re-link them manually.
-- ─────────────────────────────────────────────────────────────────────────────
UPDATE construction_estimate_line child
SET parent_line_id = parent.id
FROM construction_estimate_line parent
WHERE child.parent_line_id IS NULL
  AND child.parent_item_number IS NOT NULL
  AND child.parent_item_number <> ''
  AND parent.estimate_id   = child.estimate_id
  AND parent.tenant_id     = child.tenant_id
  AND parent.item_number   = child.parent_item_number
  AND parent.parent_item_number IS NULL -- only link to top-level parents
  AND parent.id            <> child.id;

-- ─────────────────────────────────────────────────────────────────────────────
-- 3) Backfill subline_seq for rows whose item_number already follows the
--    "N-M" pattern (e.g., "32-1", "E1-28-1"). We extract only the trailing
--    integer after the LAST dash so codes like "E1-28-1" become 1 (not 28).
-- ─────────────────────────────────────────────────────────────────────────────
UPDATE construction_estimate_line
SET subline_seq = CAST(regexp_replace(item_number, '^.*-', '') AS INTEGER)
WHERE parent_line_id IS NOT NULL
  AND subline_seq = 0
  AND item_number ~ '-[0-9]+$';

-- ─────────────────────────────────────────────────────────────────────────────
-- 4) For the remaining sub-line rows that still have subline_seq = 0 (because
--    their item_number didn't match the pattern, e.g., "LABOR", "МАШ", or was
--    empty), assign them sequential numbers starting from MAX(parsed)+1 per
--    parent. This guarantees uniqueness before the UNIQUE index is created.
-- ─────────────────────────────────────────────────────────────────────────────
WITH max_used AS (
    SELECT parent_line_id, MAX(subline_seq) AS max_seq
    FROM construction_estimate_line
    WHERE parent_line_id IS NOT NULL
    GROUP BY parent_line_id
),
to_number AS (
    SELECT
        c.id,
        COALESCE(mu.max_seq, 0)
            + ROW_NUMBER() OVER (PARTITION BY c.parent_line_id ORDER BY c.id)
        AS new_seq
    FROM construction_estimate_line c
    LEFT JOIN max_used mu ON mu.parent_line_id = c.parent_line_id
    WHERE c.parent_line_id IS NOT NULL
      AND c.subline_seq = 0
)
UPDATE construction_estimate_line AS c
SET subline_seq = to_number.new_seq
FROM to_number
WHERE c.id = to_number.id;

-- ─────────────────────────────────────────────────────────────────────────────
-- 5) Defensive: if any (parent_line_id, subline_seq) collisions still exist
--    (e.g., two rows that both parsed to the same trailing integer), push the
--    duplicates to fresh numbers starting from MAX+1 per parent. This loop
--    only runs for pathological legacy data; normal tenants land here with
--    zero affected rows.
-- ─────────────────────────────────────────────────────────────────────────────
WITH dup AS (
    SELECT
        c.id,
        c.parent_line_id,
        ROW_NUMBER() OVER (PARTITION BY c.parent_line_id, c.subline_seq ORDER BY c.id) AS dup_rank
    FROM construction_estimate_line c
    WHERE c.parent_line_id IS NOT NULL
),
dedup_max AS (
    SELECT parent_line_id, MAX(subline_seq) AS max_seq
    FROM construction_estimate_line
    WHERE parent_line_id IS NOT NULL
    GROUP BY parent_line_id
),
reassign AS (
    SELECT
        dup.id,
        dedup_max.max_seq
            + ROW_NUMBER() OVER (PARTITION BY dup.parent_line_id ORDER BY dup.id)
        AS new_seq
    FROM dup
    JOIN dedup_max ON dedup_max.parent_line_id = dup.parent_line_id
    WHERE dup.dup_rank > 1
)
UPDATE construction_estimate_line AS c
SET subline_seq = reassign.new_seq
FROM reassign
WHERE c.id = reassign.id;

-- ─────────────────────────────────────────────────────────────────────────────
-- 6) Indexes — last, once every child row has a unique subline_seq per parent
-- ─────────────────────────────────────────────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_estimate_line_parent_line
    ON construction_estimate_line (parent_line_id)
    WHERE parent_line_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_estimate_line_parent_seq
    ON construction_estimate_line (parent_line_id, subline_seq)
    WHERE parent_line_id IS NOT NULL;
