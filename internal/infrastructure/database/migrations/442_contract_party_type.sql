-- Make contract "type" real: a contract now has a counterparty kind
-- (customer / vendor / partner / lease) instead of always being a vendor.
-- For lease contracts the counterparty is a fixed asset (asset_id); for the
-- others it's a contact (existing vendor_id column, which already references
-- contacts and is nullable).

ALTER TABLE procurement_contracts
    ADD COLUMN IF NOT EXISTS party_type VARCHAR(20) NOT NULL DEFAULT 'vendor';

ALTER TABLE procurement_contracts
    ADD COLUMN IF NOT EXISTS asset_id UUID REFERENCES fixed_assets(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_procurement_contracts_party_type
    ON procurement_contracts (tenant_id, party_type);
