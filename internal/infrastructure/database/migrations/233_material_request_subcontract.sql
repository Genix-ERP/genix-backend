-- Link material requests to subcontractors for billing
ALTER TABLE construction_material_requests
  ADD COLUMN IF NOT EXISTS bill_subcontractor BOOLEAN DEFAULT false,
  ADD COLUMN IF NOT EXISTS subcontract_id BIGINT REFERENCES construction_subcontract(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_material_req_subcontract ON construction_material_requests(subcontract_id);

-- Link expense lines to subcontractors and material requests
ALTER TABLE construction_expense_lines
  ADD COLUMN IF NOT EXISTS subcontract_id BIGINT REFERENCES construction_subcontract(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS material_request_id BIGINT REFERENCES construction_material_requests(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_expense_line_subcontract ON construction_expense_lines(subcontract_id);
CREATE INDEX IF NOT EXISTS idx_expense_line_material_req ON construction_expense_lines(material_request_id);
