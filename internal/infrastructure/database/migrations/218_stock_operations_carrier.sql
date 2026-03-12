-- Add carrier, delivery address, and tracking number to stock operations
-- Used for delivery operations to track shipment details
ALTER TABLE stock_operations
    ADD COLUMN IF NOT EXISTS carrier_id UUID REFERENCES carriers(id),
    ADD COLUMN IF NOT EXISTS delivery_address TEXT,
    ADD COLUMN IF NOT EXISTS tracking_number VARCHAR(255);
