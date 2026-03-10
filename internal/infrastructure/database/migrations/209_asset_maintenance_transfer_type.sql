-- Add 'transfer' to asset_maintenance maintenance_type check constraint
ALTER TABLE asset_maintenance DROP CONSTRAINT IF EXISTS asset_maintenance_maintenance_type_check;
ALTER TABLE asset_maintenance ADD CONSTRAINT asset_maintenance_maintenance_type_check
    CHECK (maintenance_type IN ('regular_to', 'capital_repair', 'modernization', 'minor_repair', 'transfer'));
