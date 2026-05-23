-- 404_crm_sync_links.sql
-- Adds the columns + tables that wire Genix's construction module to the
-- Yuksalish CRM's project/block/block_progress hierarchy.
--
-- Linking model (set by the user, one time, in the UI):
--   construction_projects.crm_project_id    -> CRM project id
--   construction_buildings.crm_block_id     -> CRM block id
--   construction_buildings.current_crm_stage-> which of CRM's 8 stages
--                                               photo reports should land on
--   construction_buildings.crm_auto_sync    -> toggle to pause auto-sync
--
-- Sync trace (set by the auto-sync hook, no user input):
--   construction_photo_reports.crm_block_progress_id -> the CRM row our
--                                                       photos landed in
--   construction_photo_reports.crm_synced_at         -> when we succeeded
--   construction_photo_reports.crm_sync_error        -> last failure msg
--
-- crm_sync_queue is the retry table — every failed sync goes there and a
-- background worker drains it with exponential backoff.

ALTER TABLE construction_projects
    ADD COLUMN IF NOT EXISTS crm_project_id INTEGER;

CREATE INDEX IF NOT EXISTS idx_construction_projects_crm_project_id
    ON construction_projects(crm_project_id)
    WHERE crm_project_id IS NOT NULL;

ALTER TABLE construction_buildings
    ADD COLUMN IF NOT EXISTS crm_block_id INTEGER,
    ADD COLUMN IF NOT EXISTS current_crm_stage TEXT NOT NULL DEFAULT 'foundation',
    ADD COLUMN IF NOT EXISTS crm_auto_sync BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS crm_last_synced_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS crm_last_sync_error TEXT;

CREATE INDEX IF NOT EXISTS idx_construction_buildings_crm_block_id
    ON construction_buildings(crm_block_id)
    WHERE crm_block_id IS NOT NULL;

ALTER TABLE construction_photo_reports
    ADD COLUMN IF NOT EXISTS crm_block_progress_id INTEGER,
    ADD COLUMN IF NOT EXISTS crm_synced_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS crm_sync_error TEXT;

CREATE TABLE IF NOT EXISTS crm_sync_queue (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID NOT NULL,
    report_id         BIGINT NOT NULL REFERENCES construction_photo_reports(id) ON DELETE CASCADE,
    building_id       BIGINT NOT NULL REFERENCES construction_buildings(id) ON DELETE CASCADE,
    status            TEXT NOT NULL DEFAULT 'pending',
    retry_count       INT NOT NULL DEFAULT 0,
    next_attempt_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_attempted_at TIMESTAMPTZ,
    last_error        TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_crm_sync_queue_due
    ON crm_sync_queue(status, next_attempt_at)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_crm_sync_queue_report
    ON crm_sync_queue(report_id);
