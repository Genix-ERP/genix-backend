CREATE TABLE IF NOT EXISTS project_files (
    id SERIAL PRIMARY KEY,
    tenant_id VARCHAR(100) NOT NULL,
    project_id INTEGER NOT NULL REFERENCES construction_projects(id) ON DELETE CASCADE,
    file_id VARCHAR(255) NOT NULL,
    file_url VARCHAR(500) NOT NULL,
    filename VARCHAR(500) NOT NULL,
    file_size BIGINT DEFAULT 0,
    mime_type VARCHAR(100) DEFAULT '',
    description TEXT DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    created_by VARCHAR(100) DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_project_files_tenant ON project_files(tenant_id);
CREATE INDEX IF NOT EXISTS idx_project_files_project ON project_files(project_id);
