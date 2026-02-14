-- Workflow automation rules
CREATE TABLE IF NOT EXISTS workflow_rules (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    category VARCHAR(50) NOT NULL, -- inventory, sales, purchase, crm, hr, finance
    trigger_type VARCHAR(50) NOT NULL, -- event, scheduled, threshold
    trigger_event VARCHAR(100), -- e.g. 'inventory.low_stock', 'invoice.overdue', 'lead.created'
    conditions JSONB DEFAULT '{}', -- rule conditions as JSON
    actions JSONB DEFAULT '[]', -- actions to perform as JSON array
    is_active BOOLEAN DEFAULT true,
    priority INTEGER DEFAULT 0,
    last_triggered_at TIMESTAMP WITH TIME ZONE,
    trigger_count INTEGER DEFAULT 0,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX idx_workflow_rules_tenant ON workflow_rules(tenant_id);
CREATE INDEX idx_workflow_rules_category ON workflow_rules(tenant_id, category);
CREATE INDEX idx_workflow_rules_active ON workflow_rules(tenant_id, is_active) WHERE deleted_at IS NULL;
CREATE INDEX idx_workflow_rules_trigger ON workflow_rules(tenant_id, trigger_event) WHERE is_active = true AND deleted_at IS NULL;

-- Workflow execution logs
CREATE TABLE IF NOT EXISTS workflow_logs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    rule_id UUID NOT NULL REFERENCES workflow_rules(id) ON DELETE CASCADE,
    trigger_data JSONB DEFAULT '{}', -- what triggered it
    actions_executed JSONB DEFAULT '[]', -- what actions were taken
    status VARCHAR(20) DEFAULT 'success', -- success, failed, partial
    error_message TEXT,
    executed_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_workflow_logs_tenant ON workflow_logs(tenant_id);
CREATE INDEX idx_workflow_logs_rule ON workflow_logs(rule_id);
CREATE INDEX idx_workflow_logs_executed ON workflow_logs(tenant_id, executed_at DESC);
