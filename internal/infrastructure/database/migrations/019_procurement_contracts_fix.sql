-- Fix procurement_contracts - rename supplier columns to vendor (if they exist)

DO $$
BEGIN
    -- Rename supplier_id to vendor_id if supplier_id exists and vendor_id doesn't
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'procurement_contracts'
               AND column_name = 'supplier_id')
       AND NOT EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'procurement_contracts'
               AND column_name = 'vendor_id') THEN
        ALTER TABLE procurement_contracts RENAME COLUMN supplier_id TO vendor_id;
    END IF;

    -- Rename supplier_name to vendor_name if supplier_name exists and vendor_name doesn't
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'procurement_contracts'
               AND column_name = 'supplier_name')
       AND NOT EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'procurement_contracts'
               AND column_name = 'vendor_name') THEN
        ALTER TABLE procurement_contracts RENAME COLUMN supplier_name TO vendor_name;
    END IF;

    -- Rename signed_by_supplier to signed_by_vendor if it exists
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'procurement_contracts'
               AND column_name = 'signed_by_supplier')
       AND NOT EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'procurement_contracts'
               AND column_name = 'signed_by_vendor') THEN
        ALTER TABLE procurement_contracts RENAME COLUMN signed_by_supplier TO signed_by_vendor;
    END IF;

    -- Rename supplier_signature_date to vendor_signature_date if it exists
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'procurement_contracts'
               AND column_name = 'supplier_signature_date')
       AND NOT EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'procurement_contracts'
               AND column_name = 'vendor_signature_date') THEN
        ALTER TABLE procurement_contracts RENAME COLUMN supplier_signature_date TO vendor_signature_date;
    END IF;
END $$;

-- Update index name (drop old, create new) - use IF EXISTS for safety
DROP INDEX IF EXISTS idx_procurement_contracts_supplier;
CREATE INDEX IF NOT EXISTS idx_procurement_contracts_vendor ON procurement_contracts(vendor_id);
