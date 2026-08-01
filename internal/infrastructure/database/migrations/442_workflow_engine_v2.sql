-- Workflow engine v2: execution-log detail columns, scheduler fired-markers
-- (per record+rule dedupe so time-based triggers fire once, not every scan),
-- and auto-pause metadata for the rate/failure guard.

-- ── workflow_logs: detail columns for the execution-log UI ──────────────────
ALTER TABLE workflow_logs ADD COLUMN IF NOT EXISTS trigger_event VARCHAR(100);
ALTER TABLE workflow_logs ADD COLUMN IF NOT EXISTS related_type VARCHAR(50);
ALTER TABLE workflow_logs ADD COLUMN IF NOT EXISTS related_id VARCHAR(100);
ALTER TABLE workflow_logs ADD COLUMN IF NOT EXISTS duration_ms INTEGER;
ALTER TABLE workflow_logs ADD COLUMN IF NOT EXISTS condition_results JSONB;

-- status filter is the primary log-view filter
CREATE INDEX IF NOT EXISTS idx_workflow_logs_status
    ON workflow_logs(tenant_id, status, executed_at DESC);

-- ── workflow_rules: auto-pause guard metadata ───────────────────────────────
ALTER TABLE workflow_rules ADD COLUMN IF NOT EXISTS auto_paused_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE workflow_rules ADD COLUMN IF NOT EXISTS paused_reason VARCHAR(50);

-- ── fired markers: one row per (rule, event, record) already fired ──────────
-- The scheduler checks/updates this atomically before firing a rule for a
-- scanned record, which is what prevents "invoice overdue" from re-firing
-- every 15 minutes for the same invoice.
CREATE TABLE IF NOT EXISTS workflow_fired_markers (
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    rule_id UUID NOT NULL REFERENCES workflow_rules(id) ON DELETE CASCADE,
    trigger_event VARCHAR(100) NOT NULL,
    record_key VARCHAR(150) NOT NULL,
    fired_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (rule_id, trigger_event, record_key)
);

CREATE INDEX IF NOT EXISTS idx_workflow_fired_markers_cleanup
    ON workflow_fired_markers(fired_at);
