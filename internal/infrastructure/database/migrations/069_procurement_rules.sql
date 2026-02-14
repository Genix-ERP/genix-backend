-- ============================================
-- PROCUREMENT RULES ENGINE
-- Configurable rules for approval workflows
-- ============================================

-- ============================================
-- PROCUREMENT RULES TABLE (rule definitions)
-- ============================================
CREATE TABLE IF NOT EXISTS procurement_rules (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    name VARCHAR(255) NOT NULL,
    description TEXT,

    -- Rule type: auto_approve, require_approval, route_to_approver, budget_check, vendor_check
    rule_type VARCHAR(50) NOT NULL,

    -- Document type: purchase_order, purchase_requisition, both
    document_type VARCHAR(50) NOT NULL DEFAULT 'both',

    -- Priority (lower = higher priority, evaluated first)
    priority INTEGER DEFAULT 100,

    -- Conditions (JSON)
    -- Example: {"amount_min": 0, "amount_max": 1000000, "vendor_ids": [], "category_ids": []}
    conditions JSONB NOT NULL DEFAULT '{}',

    -- Actions (JSON)
    -- Example: {"action": "auto_approve"} or {"action": "route", "approver_ids": ["uuid1", "uuid2"], "approval_type": "sequential"}
    actions JSONB NOT NULL DEFAULT '{}',

    is_active BOOLEAN DEFAULT true,

    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(tenant_id, name)
);

-- ============================================
-- APPROVAL WORKFLOW INSTANCES (active approval flows)
-- ============================================
CREATE TABLE IF NOT EXISTS approval_workflow_instances (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Reference to the document
    document_type VARCHAR(50) NOT NULL, -- purchase_order, purchase_requisition
    document_id UUID NOT NULL,
    document_number VARCHAR(50),

    -- Rule that triggered this workflow
    rule_id UUID REFERENCES procurement_rules(id),
    rule_name VARCHAR(255),

    -- Status: pending, approved, rejected, cancelled
    status VARCHAR(30) DEFAULT 'pending',

    -- Approval type: sequential, parallel, any_one
    approval_type VARCHAR(30) DEFAULT 'sequential',

    -- Current step (for sequential)
    current_step INTEGER DEFAULT 1,
    total_steps INTEGER DEFAULT 1,

    -- Timestamps
    started_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMP WITH TIME ZONE,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- ============================================
-- APPROVAL WORKFLOW STEPS (individual approver actions)
-- ============================================
CREATE TABLE IF NOT EXISTS approval_workflow_steps (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    workflow_id UUID NOT NULL REFERENCES approval_workflow_instances(id) ON DELETE CASCADE,

    step_number INTEGER NOT NULL,

    -- Approver
    approver_id UUID NOT NULL REFERENCES users(id),
    approver_name VARCHAR(255),

    -- Status: pending, approved, rejected, skipped
    status VARCHAR(30) DEFAULT 'pending',

    -- Action details
    action_date TIMESTAMP WITH TIME ZONE,
    comments TEXT,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- ============================================
-- RULE EVALUATION LOG (audit trail)
-- ============================================
CREATE TABLE IF NOT EXISTS rule_evaluation_logs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    document_type VARCHAR(50) NOT NULL,
    document_id UUID NOT NULL,
    document_number VARCHAR(50),

    -- Rules evaluated
    rules_evaluated JSONB, -- Array of {rule_id, rule_name, matched: bool, reason: string}

    -- Final decision
    final_action VARCHAR(50), -- auto_approved, routed, blocked, warning
    final_action_details JSONB,

    evaluated_by UUID REFERENCES users(id),
    evaluated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- ============================================
-- INDEXES
-- ============================================
CREATE INDEX IF NOT EXISTS idx_proc_rules_tenant ON procurement_rules(tenant_id);
CREATE INDEX IF NOT EXISTS idx_proc_rules_type ON procurement_rules(rule_type);
CREATE INDEX IF NOT EXISTS idx_proc_rules_active ON procurement_rules(is_active);
CREATE INDEX IF NOT EXISTS idx_proc_rules_priority ON procurement_rules(priority);

CREATE INDEX IF NOT EXISTS idx_approval_wf_tenant ON approval_workflow_instances(tenant_id);
CREATE INDEX IF NOT EXISTS idx_approval_wf_doc ON approval_workflow_instances(document_type, document_id);
CREATE INDEX IF NOT EXISTS idx_approval_wf_status ON approval_workflow_instances(status);

CREATE INDEX IF NOT EXISTS idx_approval_steps_wf ON approval_workflow_steps(workflow_id);
CREATE INDEX IF NOT EXISTS idx_approval_steps_approver ON approval_workflow_steps(approver_id);

CREATE INDEX IF NOT EXISTS idx_rule_eval_tenant ON rule_evaluation_logs(tenant_id);
CREATE INDEX IF NOT EXISTS idx_rule_eval_doc ON rule_evaluation_logs(document_type, document_id);

-- ============================================
-- TRIGGERS
-- ============================================

-- Update procurement_rules.updated_at
DROP TRIGGER IF EXISTS trigger_procurement_rules_updated_at ON procurement_rules;
CREATE TRIGGER trigger_procurement_rules_updated_at
    BEFORE UPDATE ON procurement_rules
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- Update approval_workflow_instances.updated_at
DROP TRIGGER IF EXISTS trigger_approval_wf_updated_at ON approval_workflow_instances;
CREATE TRIGGER trigger_approval_wf_updated_at
    BEFORE UPDATE ON approval_workflow_instances
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- ============================================
-- COMMENTS
-- ============================================
COMMENT ON TABLE procurement_rules IS 'Configurable procurement rules for approval workflows';
COMMENT ON TABLE approval_workflow_instances IS 'Active approval workflows for documents';
COMMENT ON TABLE approval_workflow_steps IS 'Individual approval steps within a workflow';
COMMENT ON TABLE rule_evaluation_logs IS 'Audit log of rule evaluations';

COMMENT ON COLUMN procurement_rules.rule_type IS 'Type: auto_approve, require_approval, route_to_approver, budget_check, vendor_check';
COMMENT ON COLUMN procurement_rules.document_type IS 'Document type this rule applies to: purchase_order, purchase_requisition, both';
COMMENT ON COLUMN procurement_rules.priority IS 'Lower number = higher priority, evaluated first';
COMMENT ON COLUMN procurement_rules.conditions IS 'JSON conditions: amount_min, amount_max, vendor_ids, category_ids, warehouse_ids';
COMMENT ON COLUMN procurement_rules.actions IS 'JSON actions: action (auto_approve/route/block/warn), approver_ids, approval_type, message';
COMMENT ON COLUMN approval_workflow_instances.approval_type IS 'Type: sequential (one after another), parallel (all at once), any_one (first to approve)';
