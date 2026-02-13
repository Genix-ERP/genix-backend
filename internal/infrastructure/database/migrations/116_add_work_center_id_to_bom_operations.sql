-- Add work_center_id FK to bom_operations table
-- This allows proper linking of operations to work centers instead of just a string name

ALTER TABLE bom_operations
ADD COLUMN IF NOT EXISTS work_center_id UUID REFERENCES work_centers(id);

-- Create index for efficient joins
CREATE INDEX IF NOT EXISTS idx_bom_operations_work_center ON bom_operations(work_center_id);

-- Update existing records to link to work centers where possible (by matching name)
UPDATE bom_operations bo
SET work_center_id = wc.id
FROM work_centers wc
WHERE bo.work_center = wc.name
  AND bo.work_center_id IS NULL
  AND wc.tenant_id = (
    SELECT pb.tenant_id FROM product_boms pb WHERE pb.id = bo.bom_id
  );
