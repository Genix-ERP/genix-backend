-- Add packing data fields to stock operation lines
-- Used during pack step for delivery operations
ALTER TABLE stock_operation_lines
    ADD COLUMN IF NOT EXISTS tracking_number VARCHAR(255),
    ADD COLUMN IF NOT EXISTS package_weight DECIMAL(10,3),
    ADD COLUMN IF NOT EXISTS package_dimensions VARCHAR(100);
