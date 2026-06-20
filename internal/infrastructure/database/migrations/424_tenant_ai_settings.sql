-- 424_tenant_ai_settings.sql
--
-- Per-tenant AI provider settings (user-supplied API key + model), used by
-- features like the purchase-receipt scanner so a tenant can plug in their own
-- OpenAI or Anthropic key instead of relying on the server-wide env config.
--
-- The api_key is stored here and is NEVER returned to the client in full — the
-- read endpoint (GET /admin/ai-settings) only returns a masked preview and a
-- has_key flag.

CREATE TABLE IF NOT EXISTS tenant_ai_settings (
    tenant_id  UUID PRIMARY KEY,
    provider   TEXT        NOT NULL DEFAULT 'openai',  -- 'openai' | 'anthropic'
    model      TEXT        NOT NULL DEFAULT '',        -- e.g. 'gpt-4o', 'claude-opus-4-8'
    api_key    TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
