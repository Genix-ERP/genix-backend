-- Add previous_rate column to exchange_rates for tracking rate changes
ALTER TABLE exchange_rates ADD COLUMN IF NOT EXISTS previous_rate DECIMAL(20,10);
ALTER TABLE exchange_rates ADD COLUMN IF NOT EXISTS rate_change DECIMAL(20,10);
ALTER TABLE exchange_rates ADD COLUMN IF NOT EXISTS rate_change_percent DECIMAL(10,4);
