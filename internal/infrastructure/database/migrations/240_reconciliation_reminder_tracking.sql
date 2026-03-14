-- Add reminder tracking columns for reconciliation acts
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'reconciliation_acts' AND column_name = 'reminder_3d_sent'
    ) THEN
        ALTER TABLE reconciliation_acts ADD COLUMN reminder_3d_sent BOOLEAN DEFAULT FALSE;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'reconciliation_acts' AND column_name = 'reminder_7d_sent'
    ) THEN
        ALTER TABLE reconciliation_acts ADD COLUMN reminder_7d_sent BOOLEAN DEFAULT FALSE;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'reconciliation_acts' AND column_name = 'response_notified'
    ) THEN
        ALTER TABLE reconciliation_acts ADD COLUMN response_notified BOOLEAN DEFAULT FALSE;
    END IF;
END $$;
