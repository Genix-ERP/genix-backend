-- Add share link expiry column to reconciliation acts
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'reconciliation_acts' AND column_name = 'share_expires_at'
    ) THEN
        ALTER TABLE reconciliation_acts ADD COLUMN share_expires_at TIMESTAMP WITH TIME ZONE;
    END IF;
END $$;
