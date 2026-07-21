-- 436_notifications_push_dispatch.sql
--
-- Adds push_sent_at to notifications so a background dispatcher can deliver a
-- mobile push for notifications inserted by paths that DON'T go through
-- createNotification (background_jobs.go, scheduler_reminders.go). Rows created
-- via createNotification stamp push_sent_at themselves (they push inline), so
-- the dispatcher only ever handles the raw-insert paths.
--
-- All EXISTING rows are marked already-processed so turning this on never
-- backfills a flood of historical notifications as push.

ALTER TABLE notifications ADD COLUMN IF NOT EXISTS push_sent_at TIMESTAMPTZ;

UPDATE notifications SET push_sent_at = created_at WHERE push_sent_at IS NULL;

-- Partial index over the (normally tiny/empty) pending set — keeps the
-- dispatcher's poll query cheap.
CREATE INDEX IF NOT EXISTS idx_notifications_push_pending
    ON notifications (created_at) WHERE push_sent_at IS NULL;
