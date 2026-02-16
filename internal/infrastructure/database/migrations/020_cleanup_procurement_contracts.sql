-- Cleanup procurement_contracts - remove duplicate supplier columns

-- Check and copy data only if columns exist
DO $$
BEGIN
    -- Copy supplier_id to vendor_id if supplier_id column exists
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'procurement_contracts'
               AND column_name = 'supplier_id') THEN
        EXECUTE 'UPDATE procurement_contracts SET vendor_id = supplier_id WHERE vendor_id IS NULL AND supplier_id IS NOT NULL';
        ALTER TABLE procurement_contracts DROP COLUMN supplier_id;
    END IF;

    -- Copy supplier_name to vendor_name if supplier_name column exists
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'procurement_contracts'
               AND column_name = 'supplier_name') THEN
        EXECUTE 'UPDATE procurement_contracts SET vendor_name = supplier_name WHERE (vendor_name IS NULL OR vendor_name = '''') AND supplier_name IS NOT NULL AND supplier_name != ''''';
        ALTER TABLE procurement_contracts DROP COLUMN supplier_name;
    END IF;

    -- Drop signed_by_supplier if it exists
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'procurement_contracts'
               AND column_name = 'signed_by_supplier') THEN
        ALTER TABLE procurement_contracts DROP COLUMN signed_by_supplier;
    END IF;

    -- Drop supplier_signature_date if it exists
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'procurement_contracts'
               AND column_name = 'supplier_signature_date') THEN
        ALTER TABLE procurement_contracts DROP COLUMN supplier_signature_date;
    END IF;
END $$;
