-- 333_stages_building_id.sql
-- Assign construction stages to specific buildings/blocks.
--
-- Up to migration 332 stages were project-scoped only. The product owner
-- wants stages to be filterable per building on the Bosqichlar tab, with
-- a building tab row above the stage list. Nullable FK keeps the existing
-- project-level rows working (they're treated as "unassigned / project-wide").

ALTER TABLE construction_stages
    ADD COLUMN IF NOT EXISTS building_id BIGINT
        REFERENCES construction_buildings(id) ON DELETE SET NULL;

-- Speed up the per-building filter on the stages list.
CREATE INDEX IF NOT EXISTS idx_construction_stages_building
    ON construction_stages (building_id)
    WHERE building_id IS NOT NULL;

-- Composite index for the common query shape: list stages for project X,
-- optionally narrowed to building Y.
CREATE INDEX IF NOT EXISTS idx_construction_stages_project_building
    ON construction_stages (project_id, building_id);
