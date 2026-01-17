-- Cleanup procurement_contracts - remove duplicate supplier columns

-- Copy data from supplier columns to vendor columns if vendor columns are empty
UPDATE procurement_contracts
SET vendor_id = supplier_id
WHERE vendor_id IS NULL AND supplier_id IS NOT NULL;

UPDATE procurement_contracts
SET vendor_name = supplier_name
WHERE (vendor_name IS NULL OR vendor_name = '') AND supplier_name IS NOT NULL AND supplier_name != '';

-- Drop the old supplier columns
ALTER TABLE procurement_contracts DROP COLUMN IF EXISTS supplier_id;
ALTER TABLE procurement_contracts DROP COLUMN IF EXISTS supplier_name;
ALTER TABLE procurement_contracts DROP COLUMN IF EXISTS signed_by_supplier;
ALTER TABLE procurement_contracts DROP COLUMN IF EXISTS supplier_signature_date;
