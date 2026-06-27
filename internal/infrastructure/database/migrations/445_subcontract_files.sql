-- 445_subcontract_files.sql
--
-- File attachments for subcontractors (Subpudratchilar). Mirrors building_files:
-- the raw file is uploaded via POST /files/upload, then a reference row is
-- stored here. Lets users attach and view a subcontractor's contract/documents.

CREATE TABLE IF NOT EXISTS subcontract_files (
    id SERIAL PRIMARY KEY,
    tenant_id VARCHAR(100) NOT NULL,
    subcontract_id BIGINT NOT NULL REFERENCES construction_subcontract(id) ON DELETE CASCADE,
    file_id VARCHAR(255) NOT NULL,
    file_url VARCHAR(500) NOT NULL,
    filename VARCHAR(500) NOT NULL,
    file_size BIGINT DEFAULT 0,
    mime_type VARCHAR(100) DEFAULT '',
    description TEXT DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    created_by VARCHAR(100) DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_subcontract_files_tenant ON subcontract_files(tenant_id);
CREATE INDEX IF NOT EXISTS idx_subcontract_files_sub ON subcontract_files(subcontract_id);
