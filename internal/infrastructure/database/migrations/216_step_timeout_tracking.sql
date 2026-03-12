-- Add timeout tracking fields to stock_operation_step_log
-- Used by background job to enforce step time limits
ALTER TABLE stock_operation_step_log
    ADD COLUMN IF NOT EXISTS timeout_notified BOOLEAN DEFAULT false,
    ADD COLUMN IF NOT EXISTS timeout_blocked BOOLEAN DEFAULT false;
