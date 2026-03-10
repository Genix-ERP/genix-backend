-- Link material requests to stock operations (deliveries)
-- When a material request is created, a delivery stock operation is auto-created.
-- Confirming happens in stock operations, not in the material request directly.

ALTER TABLE construction_material_requests
  ADD COLUMN IF NOT EXISTS stock_operation_id UUID REFERENCES stock_operations(id);

CREATE INDEX IF NOT EXISTS idx_cmr_stock_operation
  ON construction_material_requests(stock_operation_id)
  WHERE stock_operation_id IS NOT NULL;
