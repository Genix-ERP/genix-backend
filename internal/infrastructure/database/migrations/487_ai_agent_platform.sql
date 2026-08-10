-- AI Yordamchi rebuild (docs/ai-yordamchi/audit.md):
--   1. ai_action_log — append-only audit trail of every AI agent action
--      (reads, denied calls, proposed/executed writes). Mirrors the
--      platform_audit_log immutability pattern (486).
--   2. tenant_agent_settings — per-tenant Agent Studio configuration for the
--      per-module agent catalog (instructions, tool toggles, auto-limits).
--      The catalog itself (agent keys, base prompts, toolsets) is code-owned
--      in internal/handler/ai_agents.go; only tenant overrides live here.
--   3. tenant_ai_settings.monthly_request_limit — per-tenant AI quota
--      override; NULL = plan default (resolved in code from
--      tenants.subscription_plan).

-- 1. AI action log ---------------------------------------------------------
CREATE TABLE IF NOT EXISTS ai_action_log (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id       UUID NOT NULL,
    organization_id UUID,
    user_id         UUID,
    agent_key       VARCHAR(50) NOT NULL DEFAULT 'orchestrator',
    conversation_id UUID,
    tool            VARCHAR(100) NOT NULL,
    args            JSONB,
    -- read | denied | write_proposed | write_executed | auto_executed | error
    kind            VARCHAR(20) NOT NULL,
    ok              BOOLEAN NOT NULL DEFAULT true,
    error           TEXT,
    result_summary  TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_ai_action_log_tenant  ON ai_action_log(tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ai_action_log_user    ON ai_action_log(user_id);
CREATE INDEX IF NOT EXISTS idx_ai_action_log_kind    ON ai_action_log(tenant_id, kind);

-- Immutability: append-only. Block UPDATE and DELETE at the DB level.
CREATE OR REPLACE FUNCTION ai_action_log_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'ai_action_log is append-only (% not allowed)', TG_OP;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_ai_action_log_no_mutate ON ai_action_log;
CREATE TRIGGER trg_ai_action_log_no_mutate
    BEFORE UPDATE OR DELETE ON ai_action_log
    FOR EACH ROW EXECUTE FUNCTION ai_action_log_immutable();

-- 2. Agent Studio: tenant overrides per catalog agent ----------------------
CREATE TABLE IF NOT EXISTS tenant_agent_settings (
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    agent_key         VARCHAR(50) NOT NULL,
    enabled           BOOLEAN NOT NULL DEFAULT true,
    -- Tenant "Ko'rsatmalar": appended to the platform prompt in a labelled
    -- <tenant_instructions> section — can narrow behaviour, never widen rights.
    instructions      TEXT NOT NULL DEFAULT '',
    -- Per-tool override: {"tool_name": "off" | "read" | "draft" | "auto"}.
    -- Effective right = this ∩ the invoking user's RBAC right (server-side).
    tool_overrides    JSONB NOT NULL DEFAULT '{}',
    -- Auto-tier ceiling (so'm) for tools switched to "auto"; NULL = auto off.
    auto_limit_amount NUMERIC(18,2),
    updated_by        UUID,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, agent_key)
);

-- 3. Per-tenant AI monthly quota override ----------------------------------
ALTER TABLE tenant_ai_settings
    ADD COLUMN IF NOT EXISTS monthly_request_limit INTEGER;

-- ai_usage_logs is the quota counter source; make the monthly count cheap.
CREATE INDEX IF NOT EXISTS idx_ai_usage_logs_tenant_month
    ON ai_usage_logs(tenant_id, created_at DESC);
