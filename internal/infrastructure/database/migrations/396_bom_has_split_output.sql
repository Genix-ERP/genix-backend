-- Move has_split_output flag to BOM level (product property, not per-order)
ALTER TABLE product_boms ADD COLUMN IF NOT EXISTS has_split_output BOOLEAN NOT NULL DEFAULT false;
