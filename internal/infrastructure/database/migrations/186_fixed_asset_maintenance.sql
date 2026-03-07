-- Add maintenance-related columns to fixed_assets
ALTER TABLE fixed_assets ADD COLUMN IF NOT EXISTS current_value DECIMAL(15,2);
ALTER TABLE fixed_assets ADD COLUMN IF NOT EXISTS remaining_months INT;
ALTER TABLE fixed_assets ADD COLUMN IF NOT EXISTS monthly_depr DECIMAL(15,2);
ALTER TABLE fixed_assets ADD COLUMN IF NOT EXISTS last_depr_date DATE;

-- Backfill current_value = acquisition_cost, remaining_months = useful_life_months
-- monthly_depr = (acquisition_cost - salvage_value) / useful_life_months for straight_line
UPDATE fixed_assets SET
    current_value = COALESCE(book_value, acquisition_cost - COALESCE(accumulated_depreciation, 0)),
    remaining_months = GREATEST(useful_life_months - COALESCE(
        (SELECT COUNT(*) FROM depreciation_entries de WHERE de.asset_id = fixed_assets.id), 0
    )::int, 0),
    monthly_depr = CASE
        WHEN useful_life_months > 0 THEN (acquisition_cost - COALESCE(salvage_value, 0)) / useful_life_months
        ELSE 0
    END
WHERE current_value IS NULL;

-- Create asset_maintenance table
CREATE TABLE IF NOT EXISTS asset_maintenance (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id             UUID NOT NULL REFERENCES tenants(id),
    organization_id       UUID REFERENCES organizations(id),
    asset_id              UUID NOT NULL REFERENCES fixed_assets(id),
    maintenance_type      VARCHAR(20) NOT NULL CHECK (maintenance_type IN ('regular_to', 'capital_repair', 'modernization', 'minor_repair')),
    service_date          DATE NOT NULL,
    cost                  DECIMAL(15,2) NOT NULL DEFAULT 0,

    -- Before maintenance
    value_before          DECIMAL(15,2) NOT NULL,
    useful_life_before    INT NOT NULL,
    monthly_depr_before   DECIMAL(15,2) NOT NULL,

    -- After maintenance (system calculates)
    value_after           DECIMAL(15,2) NOT NULL,
    useful_life_after     INT NOT NULL,
    monthly_depr_after    DECIMAL(15,2) NOT NULL,

    -- Change amounts
    life_extension_months INT DEFAULT 0,
    value_increase        DECIMAL(15,2) DEFAULT 0,

    description           TEXT,
    performed_by          VARCHAR(255),
    document_number       VARCHAR(100),
    created_by            UUID REFERENCES users(id),
    created_at            TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_asset_maintenance_asset ON asset_maintenance(asset_id);
CREATE INDEX IF NOT EXISTS idx_asset_maintenance_date ON asset_maintenance(service_date);
