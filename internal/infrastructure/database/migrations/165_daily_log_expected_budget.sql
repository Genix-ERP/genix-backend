-- Migration 165: Add expected_budget to construction_daily_log

ALTER TABLE construction_daily_log
    ADD COLUMN IF NOT EXISTS expected_budget DECIMAL(18,2) NOT NULL DEFAULT 0;
