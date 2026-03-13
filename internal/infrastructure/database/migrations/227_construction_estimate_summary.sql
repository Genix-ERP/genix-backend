CREATE TABLE IF NOT EXISTS construction_estimate_summary (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL,
    project_id BIGINT NOT NULL REFERENCES construction_projects(id) ON DELETE CASCADE,
    batch_id VARCHAR(50) NOT NULL,
    row_number INT NOT NULL DEFAULT 0,
    category_name VARCHAR(500) NOT NULL,
    building_column VARCHAR(255) NOT NULL,
    amount DECIMAL(18,2) DEFAULT 0,
    created_date TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_estimate_summary_project ON construction_estimate_summary(project_id, tenant_id);
CREATE INDEX IF NOT EXISTS idx_estimate_summary_batch ON construction_estimate_summary(batch_id);
