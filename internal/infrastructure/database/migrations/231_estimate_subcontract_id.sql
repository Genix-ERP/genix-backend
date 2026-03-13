-- Link estimates to subcontractors
ALTER TABLE construction_estimate
  ADD COLUMN IF NOT EXISTS subcontract_id BIGINT REFERENCES construction_subcontract(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_estimate_subcontract ON construction_estimate(subcontract_id);
