-- Ensure track_inventory column exists (it was added in 002_erp_modules.sql but may be
-- missing in databases that were created from an older snapshot).
ALTER TABLE products ADD COLUMN IF NOT EXISTS track_inventory BOOLEAN NOT NULL DEFAULT true;

COMMENT ON COLUMN products.track_inventory IS 'When false, inventory quantity is not deducted during production (consumable/infinite supply items like water, gas)';
