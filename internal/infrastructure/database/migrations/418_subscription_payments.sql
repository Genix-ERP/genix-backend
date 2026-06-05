-- Subscription payments table: tracks Multicard payment attempts for subscriptions
CREATE TABLE IF NOT EXISTS subscription_payments (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id),
    plan            VARCHAR(50)  NOT NULL,
    amount_uzs      BIGINT       NOT NULL,  -- in UZS (not tiyin)
    amount_tiyin    BIGINT       NOT NULL,  -- amount * 100
    invoice_id      VARCHAR(255) NOT NULL UNIQUE, -- our internal ID sent to Multicard
    multicard_uuid  VARCHAR(255),           -- UUID returned by Multicard
    checkout_url    TEXT,
    status          VARCHAR(50)  NOT NULL DEFAULT 'pending', -- pending, success, error, revert
    billing_id      VARCHAR(255),
    card_pan        VARCHAR(20),
    ps              VARCHAR(50),            -- uzcard, humo, visa, mastercard
    card_token      VARCHAR(255),
    payment_time    VARCHAR(50),
    receipt_url     TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sub_payments_tenant ON subscription_payments(tenant_id);
CREATE INDEX IF NOT EXISTS idx_sub_payments_invoice ON subscription_payments(invoice_id);
CREATE INDEX IF NOT EXISTS idx_sub_payments_multicard_uuid ON subscription_payments(multicard_uuid);
