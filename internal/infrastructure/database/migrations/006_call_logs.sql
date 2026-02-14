-- Migration: 005_call_logs
-- Description: Create call_logs table for CRM PBX integration

-- Call logs table
CREATE TABLE IF NOT EXISTS call_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    contact_id UUID REFERENCES contacts(id) ON DELETE SET NULL,
    contact_person_id UUID REFERENCES contact_persons(id) ON DELETE SET NULL,
    caller_number VARCHAR(50) NOT NULL,
    receiver_number VARCHAR(50),
    call_type VARCHAR(20) NOT NULL DEFAULT 'outbound',
    call_start_time TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    call_end_time TIMESTAMP,
    call_duration INTEGER DEFAULT 0,
    call_outcome VARCHAR(30) DEFAULT 'initiated',
    recording_url TEXT,
    notes TEXT,
    sentiment_score DECIMAL(3, 2),
    summary TEXT,
    pbx_call_id VARCHAR(100),
    agent_id UUID REFERENCES users(id) ON DELETE SET NULL,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP,

    CONSTRAINT chk_call_type CHECK (call_type IN ('inbound', 'outbound', 'missed')),
    CONSTRAINT chk_call_outcome CHECK (call_outcome IN ('answered', 'no_answer', 'busy', 'voicemail', 'failed', 'completed', 'initiated')),
    CONSTRAINT chk_sentiment_score CHECK (sentiment_score IS NULL OR (sentiment_score >= -1 AND sentiment_score <= 1)),
    CONSTRAINT chk_call_duration CHECK (call_duration >= 0)
);

-- Indexes for efficient querying
CREATE INDEX IF NOT EXISTS idx_call_logs_tenant ON call_logs(tenant_id);
CREATE INDEX IF NOT EXISTS idx_call_logs_contact ON call_logs(tenant_id, contact_id);
CREATE INDEX IF NOT EXISTS idx_call_logs_agent ON call_logs(tenant_id, agent_id);
CREATE INDEX IF NOT EXISTS idx_call_logs_call_type ON call_logs(tenant_id, call_type);
CREATE INDEX IF NOT EXISTS idx_call_logs_start_time ON call_logs(tenant_id, call_start_time DESC);
CREATE INDEX IF NOT EXISTS idx_call_logs_pbx_call_id ON call_logs(tenant_id, pbx_call_id);
CREATE INDEX IF NOT EXISTS idx_call_logs_deleted ON call_logs(tenant_id, deleted_at) WHERE deleted_at IS NULL;

-- Trigger to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_call_logs_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trigger_call_logs_updated_at ON call_logs;
CREATE TRIGGER trigger_call_logs_updated_at
    BEFORE UPDATE ON call_logs
    FOR EACH ROW
    EXECUTE FUNCTION update_call_logs_updated_at();

-- Comments
COMMENT ON TABLE call_logs IS 'Stores call logs for CRM PBX integration';
COMMENT ON COLUMN call_logs.call_type IS 'Type of call: inbound, outbound, or missed';
COMMENT ON COLUMN call_logs.call_outcome IS 'Outcome of the call: answered, no_answer, busy, voicemail, failed, completed, initiated';
COMMENT ON COLUMN call_logs.call_duration IS 'Duration of call in seconds';
COMMENT ON COLUMN call_logs.sentiment_score IS 'AI-analyzed sentiment score from -1 (negative) to 1 (positive)';
COMMENT ON COLUMN call_logs.summary IS 'AI-generated summary of the call';
COMMENT ON COLUMN call_logs.pbx_call_id IS 'External call ID from PBX system';
