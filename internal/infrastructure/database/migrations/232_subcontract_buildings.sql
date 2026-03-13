-- Junction table for subcontract-building many-to-many relationship
CREATE TABLE IF NOT EXISTS construction_subcontract_buildings (
    id BIGSERIAL PRIMARY KEY,
    subcontract_id BIGINT NOT NULL REFERENCES construction_subcontract(id) ON DELETE CASCADE,
    building_id BIGINT NOT NULL REFERENCES construction_buildings(id) ON DELETE CASCADE,
    UNIQUE(subcontract_id, building_id)
);

CREATE INDEX IF NOT EXISTS idx_subcontract_buildings_sub ON construction_subcontract_buildings(subcontract_id);
CREATE INDEX IF NOT EXISTS idx_subcontract_buildings_bld ON construction_subcontract_buildings(building_id);
