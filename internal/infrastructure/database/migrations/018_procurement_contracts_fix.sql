-- Fix procurement_contracts - rename supplier columns to vendor

-- Rename columns
ALTER TABLE procurement_contracts RENAME COLUMN supplier_id TO vendor_id;
ALTER TABLE procurement_contracts RENAME COLUMN supplier_name TO vendor_name;
ALTER TABLE procurement_contracts RENAME COLUMN signed_by_supplier TO signed_by_vendor;
ALTER TABLE procurement_contracts RENAME COLUMN supplier_signature_date TO vendor_signature_date;

-- Update index name (drop old, create new)
DROP INDEX IF EXISTS idx_procurement_contracts_supplier;
CREATE INDEX IF NOT EXISTS idx_procurement_contracts_vendor ON procurement_contracts(vendor_id);

-- Update comments
COMMENT ON COLUMN procurement_contracts.vendor_id IS 'Vendor/Supplier ID';
COMMENT ON COLUMN procurement_contracts.vendor_name IS 'Vendor/Supplier name';
