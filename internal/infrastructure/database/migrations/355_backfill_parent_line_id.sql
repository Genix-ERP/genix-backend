-- 355_backfill_parent_line_id.sql
--
-- One-time backfill of construction_estimate_line.parent_line_id for
-- children that were imported via the old BulkCreateEstimateLines path.
--
-- Background: the bulk import wrote parent_item_number (the parent's
-- local item-number, e.g. "648" or "1.1") but never populated the
-- parent_line_id FK. Both the Smeta boshqaruvi tab AND the v2 Bosqichlar
-- tab group sub-resources by parent_line_id, so without the FK every
-- imported work showed "0 resurs" even though the children were sitting
-- right there in the table.
--
-- Two-column update: we need to set parent_line_id AND subline_seq in
-- the same statement because migration 332 added the unique index
--   uq_estimate_line_parent_seq (parent_line_id, subline_seq) WHERE parent_line_id IS NOT NULL
-- All bulk-imported children currently carry subline_seq = 0 (the
-- default), so naively assigning the same parent_line_id to every child
-- of a work would collide on (parent, 0). We assign a fresh sequence
-- per parent using ROW_NUMBER() over sort_order so each child gets a
-- unique seq within its parent (1, 2, 3, …).
--
-- Resolution rule: for each child line where
--     parent_line_id IS NULL
--     AND COALESCE(resource_type, '') <> ''
--     AND parent_item_number ~ '^[0-9]+([.][0-9]+)?$'
-- find the immediately-preceding top-level work (resource_type empty)
-- with item_number = child.parent_item_number, lower sort_order, in the
-- same estimate. The "immediately preceding" rule disambiguates the
-- (rare) case where item-numbers reset between sections.

WITH resolved AS (
    SELECT
        child.id            AS child_id,
        parent.parent_id    AS parent_id,
        ROW_NUMBER() OVER (
            PARTITION BY parent.parent_id
            ORDER BY child.sort_order, child.id
        ) AS new_seq
    FROM construction_estimate_line child
    CROSS JOIN LATERAL (
        SELECT p.id AS parent_id
        FROM construction_estimate_line p
        WHERE p.estimate_id = child.estimate_id
          AND p.tenant_id   = child.tenant_id
          AND p.item_number = child.parent_item_number
          AND p.sort_order  < child.sort_order
          AND COALESCE(p.resource_type, '') = ''
        ORDER BY p.sort_order DESC
        LIMIT 1
    ) parent
    WHERE child.parent_line_id IS NULL
      AND COALESCE(child.resource_type, '') <> ''
      AND child.parent_item_number ~ '^[0-9]+([.][0-9]+)?$'
)
UPDATE construction_estimate_line el
SET parent_line_id = r.parent_id,
    subline_seq    = r.new_seq
FROM resolved r
WHERE el.id = r.child_id;
