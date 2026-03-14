-- Audit log for Reja vs Fakt changes (materials & equipment)
CREATE TABLE IF NOT EXISTS construction_reja_fakt_audit (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL,
    project_id BIGINT NOT NULL,
    item_type VARCHAR(20) NOT NULL, -- 'material' or 'equipment'
    item_id BIGINT NOT NULL,
    item_name VARCHAR(500) NOT NULL,
    sub_stage_id BIGINT NOT NULL,
    action VARCHAR(20) NOT NULL, -- 'create', 'update', 'delete'
    changes JSONB, -- {field: {old: x, new: y}, ...}
    user_id UUID,
    user_name VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_reja_fakt_audit_item ON construction_reja_fakt_audit(item_type, item_id);
CREATE INDEX IF NOT EXISTS idx_reja_fakt_audit_project ON construction_reja_fakt_audit(tenant_id, project_id);
CREATE INDEX IF NOT EXISTS idx_reja_fakt_audit_sub_stage ON construction_reja_fakt_audit(sub_stage_id);
