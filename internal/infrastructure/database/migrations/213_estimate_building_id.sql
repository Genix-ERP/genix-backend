ALTER TABLE construction_estimate
    ADD COLUMN building_id BIGINT REFERENCES construction_buildings(id) ON DELETE SET NULL;

CREATE INDEX idx_construction_estimate_building ON construction_estimate(building_id) WHERE building_id IS NOT NULL;
