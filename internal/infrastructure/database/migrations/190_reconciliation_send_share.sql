-- Add columns for send/share functionality on reconciliation acts
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'reconciliation_acts' AND column_name = 'share_token'
    ) THEN
        ALTER TABLE reconciliation_acts ADD COLUMN share_token VARCHAR(64) UNIQUE;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'reconciliation_acts' AND column_name = 'sent_at'
    ) THEN
        ALTER TABLE reconciliation_acts ADD COLUMN sent_at TIMESTAMP WITH TIME ZONE;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'reconciliation_acts' AND column_name = 'sent_via'
    ) THEN
        ALTER TABLE reconciliation_acts ADD COLUMN sent_via VARCHAR(20); -- 'email', 'whatsapp'
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'reconciliation_acts' AND column_name = 'sent_to'
    ) THEN
        ALTER TABLE reconciliation_acts ADD COLUMN sent_to VARCHAR(255);
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_reconciliation_acts_share_token ON reconciliation_acts(share_token) WHERE share_token IS NOT NULL;
