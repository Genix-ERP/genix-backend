-- Tender Platform: Notifications
CREATE TABLE IF NOT EXISTS tender_notifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES tender_company_profiles(id) ON DELETE CASCADE,
    type VARCHAR(50) NOT NULL,
    title VARCHAR(255) NOT NULL,
    message TEXT,
    data JSONB,
    is_read BOOLEAN NOT NULL DEFAULT FALSE,
    read_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tender_notifications_user ON tender_notifications(user_id);
CREATE INDEX IF NOT EXISTS idx_tender_notifications_read ON tender_notifications(user_id, is_read);
CREATE INDEX IF NOT EXISTS idx_tender_notifications_created ON tender_notifications(created_at DESC);
