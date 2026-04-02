-- Add pipeline_type to distinguish lead stages from opportunity stages
-- Add organization_id so stages can be scoped per company
ALTER TABLE pipeline_stages ADD COLUMN IF NOT EXISTS pipeline_type VARCHAR(20) NOT NULL DEFAULT 'opportunity';
ALTER TABLE pipeline_stages ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);

-- Existing records are opportunity stages, leave them as-is
-- Create index for efficient queries
CREATE INDEX IF NOT EXISTS idx_pipeline_stages_type_org
ON pipeline_stages (tenant_id, pipeline_type, organization_id)
WHERE is_active = true;
