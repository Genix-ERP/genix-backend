CREATE TABLE IF NOT EXISTS building_files (
    id SERIAL PRIMARY KEY,
    tenant_id VARCHAR(100) NOT NULL,
    building_id INTEGER NOT NULL REFERENCES construction_buildings(id) ON DELETE CASCADE,
    file_id VARCHAR(255) NOT NULL,
    file_url VARCHAR(500) NOT NULL,
    filename VARCHAR(500) NOT NULL,
    file_size BIGINT DEFAULT 0,
    mime_type VARCHAR(100) DEFAULT '',
    description TEXT DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    created_by VARCHAR(100) DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_building_files_tenant ON building_files(tenant_id);
CREATE INDEX IF NOT EXISTS idx_building_files_building ON building_files(building_id);
