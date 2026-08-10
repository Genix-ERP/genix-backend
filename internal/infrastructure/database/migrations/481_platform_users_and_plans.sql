-- Phase 3/4 (docs/admin-panel): a SEPARATE control-plane identity for platform
-- staff (never rows in the tenant users/roles tables) + a real plan catalog.

-- ============================================
-- PLATFORM USERS (control-plane identity)
-- ============================================
CREATE TABLE IF NOT EXISTS platform_users (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    email         VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    first_name    VARCHAR(100) NOT NULL DEFAULT '',
    last_name     VARCHAR(100) NOT NULL DEFAULT '',
    -- Fixed, code-enforced roles: super_admin | admin | manejer | tex_podderjka
    role          VARCHAR(30)  NOT NULL DEFAULT 'admin',
    is_active     BOOLEAN      NOT NULL DEFAULT true,
    totp_secret   VARCHAR(64),                 -- base32; NULL until enrolled
    totp_enabled  BOOLEAN      NOT NULL DEFAULT false,
    ip_allowlist  TEXT[],                       -- optional per-user IP allowlist
    last_login_at TIMESTAMPTZ,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at    TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_platform_users_email ON platform_users(lower(email)) WHERE deleted_at IS NULL;

-- Backfill: every current is_system_admin tenant user becomes a platform
-- super_admin, so nobody is locked out of the panel after the split. They can
-- log in at /platform/auth/login with the SAME email + password.
INSERT INTO platform_users (email, password_hash, first_name, last_name, role, is_active)
SELECT DISTINCT ON (lower(email)) email, password_hash, first_name, last_name, 'super_admin', true
FROM users
WHERE is_system_admin = true AND deleted_at IS NULL AND email IS NOT NULL AND email <> ''
ON CONFLICT (email) DO NOTHING;

-- ============================================
-- PLAN CATALOG
-- ============================================
CREATE TABLE IF NOT EXISTS platform_plans (
    id                     UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    code                   VARCHAR(50) UNIQUE NOT NULL,
    display_name           VARCHAR(100) NOT NULL,
    price_per_user_monthly BIGINT  NOT NULL DEFAULT 0,   -- UZS
    included_users         INT     NOT NULL DEFAULT 1,
    max_users              INT,                           -- NULL = unlimited
    ai_quota               INT,                           -- monthly AI requests; NULL = unlimited
    features               JSONB   NOT NULL DEFAULT '[]',
    trial_days             INT     NOT NULL DEFAULT 7,
    grace_days             INT     NOT NULL DEFAULT 30,
    is_active              BOOLEAN NOT NULL DEFAULT true,
    sort_order             INT     NOT NULL DEFAULT 0,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO platform_plans
    (code, display_name, price_per_user_monthly, included_users, max_users, ai_quota, trial_days, grace_days, sort_order)
VALUES
    ('free',         'Bepul',       0,      3,  3,    50,   7,  30, 0),
    ('starter',      'Starter',     99000,  5,  10,   500,  14, 30, 1),
    ('professional', 'Professional',199000, 10, 100,  5000, 14, 30, 2),
    ('enterprise',   'Korporativ',  299000, 25, NULL, NULL, 30, 30, 3)
ON CONFLICT (code) DO NOTHING;
