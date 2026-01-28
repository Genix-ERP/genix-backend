-- Add is_base_currency column to currencies table
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'currencies' AND column_name = 'is_base_currency') THEN
        ALTER TABLE currencies ADD COLUMN is_base_currency BOOLEAN DEFAULT false;
    END IF;
END $$;

-- Set UZS as base currency if it exists
UPDATE currencies SET is_base_currency = true WHERE code = 'UZS';

-- Create index for base currency lookup
CREATE INDEX IF NOT EXISTS idx_currencies_is_base ON currencies(is_base_currency) WHERE is_base_currency = true;
