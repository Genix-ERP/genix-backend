-- Add source reference columns to stock_operations for bi-directional sync
-- between stock operations and their originating documents (PO, SO, etc.)

ALTER TABLE stock_operations
  ADD COLUMN IF NOT EXISTS source_type VARCHAR(50),
  ADD COLUMN IF NOT EXISTS source_id UUID;

CREATE INDEX IF NOT EXISTS idx_stock_ops_source ON stock_operations(source_type, source_id) WHERE source_id IS NOT NULL;
