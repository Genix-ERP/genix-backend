ALTER TABLE construction_estimate_line
  ADD COLUMN IF NOT EXISTS resource_type VARCHAR(50),
  ADD COLUMN IF NOT EXISTS parent_item_number VARCHAR(50);
