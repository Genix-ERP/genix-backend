-- Migration 324: Generic webhooks infrastructure
-- Reference: TT Buxgalteriya ERP §7.4 — "Webhook'lar orqali tashqi tizimlarga voqealar jo'natish"
--
-- Tenants configure webhook subscriptions (URL + event list + secret). The app
-- layer dispatches events to subscribed URLs asynchronously and records every
-- delivery attempt for debugging and retry. HMAC-SHA256 signature is computed
-- with the shared secret and sent in X-Genix-Signature.

CREATE TABLE IF NOT EXISTS webhook_subscriptions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    url TEXT NOT NULL,
    events TEXT[] NOT NULL DEFAULT '{}',
        -- e.g. ['journal_entry.posted', 'journal_entry.reversed',
        --       'period.closed', 'einvoice.approved', 'payment.confirmed']
    secret TEXT,
    is_active BOOLEAN NOT NULL DEFAULT true,
    max_retries INT NOT NULL DEFAULT 5,
    timeout_ms INT NOT NULL DEFAULT 10000,
    last_triggered_at TIMESTAMPTZ,
    last_status VARCHAR(20),  -- success | failed | timeout
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_webhook_subs_tenant_active
    ON webhook_subscriptions (tenant_id, is_active)
    WHERE is_active = true;
CREATE INDEX IF NOT EXISTS idx_webhook_subs_events
    ON webhook_subscriptions USING GIN (events);

CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    subscription_id UUID NOT NULL REFERENCES webhook_subscriptions(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL,
    event_name VARCHAR(128) NOT NULL,
    event_id UUID NOT NULL,
    payload JSONB NOT NULL,
    request_signature TEXT,
    attempt_number INT NOT NULL DEFAULT 1,
    response_status INT,
    response_body TEXT,
    error_message TEXT,
    duration_ms INT,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
        -- pending | success | failed | retrying | abandoned
    scheduled_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    next_retry_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_webhook_del_subscription
    ON webhook_deliveries (subscription_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_webhook_del_pending_retry
    ON webhook_deliveries (status, next_retry_at)
    WHERE status IN ('pending', 'retrying');
CREATE INDEX IF NOT EXISTS idx_webhook_del_event_id
    ON webhook_deliveries (event_id);

COMMENT ON TABLE webhook_subscriptions IS
    'TT Buxgalteriya §7.4: per-tenant webhook subscriptions with event filters.';
COMMENT ON TABLE webhook_deliveries IS
    'Log of every webhook delivery attempt with full request/response for audit and retry.';
