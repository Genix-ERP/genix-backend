-- Add overdue_notified flag to purchase_invoices for vendor bill overdue notifications
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'purchase_invoices' AND column_name = 'overdue_notified'
    ) THEN
        ALTER TABLE purchase_invoices ADD COLUMN overdue_notified BOOLEAN DEFAULT FALSE;
    END IF;
END $$;
