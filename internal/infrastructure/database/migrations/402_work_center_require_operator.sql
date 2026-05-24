-- When true, starting a work order on this work center requires selecting an operator
ALTER TABLE work_centers ADD COLUMN IF NOT EXISTS require_operator BOOLEAN NOT NULL DEFAULT false;
