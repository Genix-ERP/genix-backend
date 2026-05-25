-- 365_construction_project_warehouse.sql
--
-- Optional default warehouse per construction project. When set, the
-- material reservation pipeline (reserveMaterialsForWork) and the
-- material request creation flow (CreateMaterialRequest) will draw
-- from this warehouse first instead of falling back to the
-- "highest stock anywhere in the tenant" auto-pick. Leaving it NULL
-- preserves today's behaviour, so this is a non-breaking change for
-- existing projects.
--
-- ON DELETE SET NULL: deleting a warehouse drops the link silently
-- and the project reverts to auto-pick on the next reservation.
-- We don't want a warehouse delete to be blocked just because a
-- project happened to reference it.

ALTER TABLE construction_projects
    ADD COLUMN IF NOT EXISTS warehouse_id UUID
        REFERENCES warehouses(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_construction_projects_warehouse
    ON construction_projects (warehouse_id)
    WHERE warehouse_id IS NOT NULL;

COMMENT ON COLUMN construction_projects.warehouse_id IS
    'Optional default warehouse for material reservations and requests. NULL = use auto-pick (highest-stock or oldest active warehouse).';
