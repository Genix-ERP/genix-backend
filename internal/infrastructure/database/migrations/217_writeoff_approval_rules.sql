-- Add configurable approval rules for write-off operations
-- Allows setting thresholds (by amount/quantity) that trigger manager approval
ALTER TABLE warehouse_operation_types
    ADD COLUMN IF NOT EXISTS approval_rule VARCHAR(20) DEFAULT 'never'
        CHECK (approval_rule IN ('never','always','by_amount','by_quantity')),
    ADD COLUMN IF NOT EXISTS approval_amount_threshold DECIMAL(20,4),
    ADD COLUMN IF NOT EXISTS approval_quantity_threshold DECIMAL(20,4);
