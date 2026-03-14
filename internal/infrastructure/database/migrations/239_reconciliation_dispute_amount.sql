-- Add dispute amount column for counterparty's stated balance
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'reconciliation_acts' AND column_name = 'dispute_amount'
    ) THEN
        ALTER TABLE reconciliation_acts ADD COLUMN dispute_amount DECIMAL(15,2);
    END IF;
END $$;
