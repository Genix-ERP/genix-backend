-- Migration 328: Add building_id to construction_act (Forma 2 / Forma 3 / Forma 19)
--
-- Forms are logically per-building/block, not per-project. Estimates already
-- carry building_id (migration 213). This column lets Forma 2 copy the
-- building_id from its source estimate, lets Forma 3 aggregate only within a
-- building, and lets the Forms list filter and group by building.
--
-- Nullable: legacy acts created before this migration have no building; also
-- project-wide F19 acts may not be tied to one building.
--
-- Note: construction_buildings has no deleted_at/is_active soft-delete column
-- (see migration 112), so we don't filter on those.

ALTER TABLE construction_act
    ADD COLUMN IF NOT EXISTS building_id BIGINT
        REFERENCES construction_buildings(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_construction_act_building
    ON construction_act (building_id)
    WHERE building_id IS NOT NULL;

-- Composite index for the very common "forms for this project + building"
-- lookup used by the Forms tab.
CREATE INDEX IF NOT EXISTS idx_construction_act_project_building
    ON construction_act (project_id, building_id, act_type);

-- Backfill: Forma 2 acts whose ks2_source_id is null but whose project has
-- exactly ONE building get that building auto-assigned. Safe heuristic —
-- multi-building projects stay NULL and must be set manually.
UPDATE construction_act a
SET building_id = b.id
FROM (
    SELECT project_id, MIN(id) AS id, COUNT(*) AS cnt
    FROM construction_buildings
    GROUP BY project_id
    HAVING COUNT(*) = 1
) b
WHERE a.project_id = b.project_id
  AND a.building_id IS NULL;

COMMENT ON COLUMN construction_act.building_id IS
    'Optional building/block this act belongs to. Forma 2 is copied from the source estimate''s building; Forma 3 aggregates within this building.';
