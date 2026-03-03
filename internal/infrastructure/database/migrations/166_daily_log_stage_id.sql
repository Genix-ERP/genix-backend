ALTER TABLE construction_daily_log
    ADD COLUMN IF NOT EXISTS stage_id BIGINT REFERENCES construction_stages(id) ON DELETE SET NULL;
