-- Add partner_name text field to subcontract (replacing partner_id organization lookup)
ALTER TABLE construction_subcontract
  ADD COLUMN IF NOT EXISTS partner_name VARCHAR(255);

-- Copy existing partner names from organizations
UPDATE construction_subcontract s
  SET partner_name = p.name
  FROM organizations p
  WHERE s.partner_id = p.id AND s.partner_name IS NULL;
