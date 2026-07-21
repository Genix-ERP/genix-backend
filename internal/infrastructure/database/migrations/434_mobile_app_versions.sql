-- 434_mobile_app_versions.sql
--
-- Global mobile-app version gate ("updater"). One row per platform. The mobile
-- app calls the PUBLIC endpoint GET /api/v1/mobile/version?platform=..&version=..
-- on launch; the server compares the client's version against these rows and
-- tells it whether an update is available and whether it is MANDATORY.
--
-- This is deliberately NOT tenant-scoped: there is a single mobile app binary
-- serving every tenant, so its version, minimum-supported version and store /
-- download link are platform-wide config owned by the system admin — managed
-- from Settings → "Mobile App". No tenant_id column, so it is untouched by any
-- per-tenant data operations.
--
--   latest_version : newest published version (offer an update below this)
--   min_version    : oldest still-allowed version (FORCE update below this)
--   force_update   : hard override — require everyone to update regardless
--   update_url     : Play Store / App Store link, or a direct APK/IPA URL

CREATE TABLE IF NOT EXISTS mobile_app_versions (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    platform       VARCHAR(20) NOT NULL UNIQUE CHECK (platform IN ('android', 'ios')),
    latest_version VARCHAR(30) NOT NULL DEFAULT '1.0.0',
    min_version    VARCHAR(30) NOT NULL DEFAULT '1.0.0',
    update_url     TEXT        NOT NULL DEFAULT '',
    release_notes  TEXT        NOT NULL DEFAULT '',
    force_update   BOOLEAN     NOT NULL DEFAULT false,
    is_active      BOOLEAN     NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by     UUID
);

-- Seed a row per platform so the admin screen has something to edit and the
-- public check has defaults (is_active=true but update_url empty => no update
-- is advertised until an admin fills it in).
INSERT INTO mobile_app_versions (platform, latest_version, min_version)
VALUES ('android', '1.0.0', '1.0.0'),
       ('ios',     '1.0.0', '1.0.0')
ON CONFLICT (platform) DO NOTHING;
