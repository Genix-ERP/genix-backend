-- 435_device_tokens.sql
--
-- Per-user device push tokens for FCM (Firebase Cloud Messaging). The mobile
-- app registers its FCM token after login (POST /api/v1/devices); the backend
-- fans a push out to every registered token for a user whenever an in-app
-- notification is created (see createNotification / pushToUser).
--
-- `token` is UNIQUE: the same physical device produces one FCM token, so a
-- token that reappears (app reinstall, or a different user logging in on that
-- device) re-homes to the new (user, tenant) via upsert instead of duplicating.
-- Dead tokens (FCM reports UNREGISTERED) are pruned automatically on send.

CREATE TABLE IF NOT EXISTS device_tokens (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id    UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform     VARCHAR(20) NOT NULL DEFAULT 'android',  -- 'android' | 'ios'
    token        TEXT        NOT NULL UNIQUE,
    device_info  JSONB       NOT NULL DEFAULT '{}',
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_device_tokens_user   ON device_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_device_tokens_tenant ON device_tokens(tenant_id);
