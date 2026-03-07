-- Add response tracking columns for counterparty confirmation/dispute
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'reconciliation_acts' AND column_name = 'response_status'
    ) THEN
        ALTER TABLE reconciliation_acts ADD COLUMN response_status VARCHAR(20); -- 'confirmed', 'disputed', 'no_response'
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'reconciliation_acts' AND column_name = 'responded_at'
    ) THEN
        ALTER TABLE reconciliation_acts ADD COLUMN responded_at TIMESTAMP WITH TIME ZONE;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'reconciliation_acts' AND column_name = 'dispute_note'
    ) THEN
        ALTER TABLE reconciliation_acts ADD COLUMN dispute_note TEXT;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'reconciliation_acts' AND column_name = 'respondent_name'
    ) THEN
        ALTER TABLE reconciliation_acts ADD COLUMN respondent_name VARCHAR(255);
    END IF;
END $$;
