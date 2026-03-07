-- Add supplier/purchase/payment fields to fixed_assets
ALTER TABLE fixed_assets ADD COLUMN IF NOT EXISTS supplier_id UUID REFERENCES contacts(id);
ALTER TABLE fixed_assets ADD COLUMN IF NOT EXISTS supplier_name VARCHAR(255);
ALTER TABLE fixed_assets ADD COLUMN IF NOT EXISTS document_number VARCHAR(100);
ALTER TABLE fixed_assets ADD COLUMN IF NOT EXISTS document_date DATE;
ALTER TABLE fixed_assets ADD COLUMN IF NOT EXISTS payment_method VARCHAR(20) DEFAULT 'cash';
ALTER TABLE fixed_assets ADD COLUMN IF NOT EXISTS total_paid DECIMAL(15,2) DEFAULT 0;

-- Add next_service_date to asset_maintenance
ALTER TABLE asset_maintenance ADD COLUMN IF NOT EXISTS next_service_date DATE;

-- Create asset_payments table for credit installments
CREATE TABLE IF NOT EXISTS asset_payments (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id),
    organization_id UUID REFERENCES organizations(id),
    asset_id        UUID NOT NULL REFERENCES fixed_assets(id),
    amount          DECIMAL(15,2) NOT NULL,
    method          VARCHAR(20) NOT NULL CHECK (method IN ('cash', 'bank')),
    paid_at         DATE NOT NULL,
    note            TEXT,
    journal_entry_id UUID REFERENCES journal_entries(id),
    created_by      UUID REFERENCES users(id),
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_asset_payments_asset ON asset_payments(asset_id);

-- Backfill total_paid for existing non-credit assets
UPDATE fixed_assets SET total_paid = acquisition_cost
WHERE total_paid = 0 AND payment_method = 'cash' AND deleted_at IS NULL;

-- Seed default asset categories if empty (per tenant, pick first org)
INSERT INTO asset_categories (id, tenant_id, organization_id, code, name, description, depreciation_method, default_useful_life_months, is_active, created_at, updated_at)
SELECT gen_random_uuid(), tnt.id, (SELECT o.id FROM organizations o WHERE o.tenant_id = tnt.id AND o.deleted_at IS NULL ORDER BY o.created_at LIMIT 1),
       t.code, t.name, t.description, 'straight_line', t.life_months, true, NOW(), NOW()
FROM tenants tnt
CROSS JOIN (VALUES
    ('BUILDINGS',   'Binolar va inshootlar',     'Ofis binolari, omborxonalar',     360),
    ('EQUIPMENT',   'Uskunalar va mashinalar',    'Ishlab chiqarish uskunalari',      60),
    ('VEHICLES',    'Transport vositalari',       'Yuk va yengil mashinalar',         60),
    ('COMPUTERS',   'Kompyuterlar va IT',         'Kompyuterlar, serverlar, tarmoq',  36),
    ('FURNITURE',   'Mebel va jihozlar',          'Ofis mebellari',                   60),
    ('INTANGIBLE',  'Nomoddiy aktivlar',          'Litsenziyalar, dasturlar',         24)
) AS t(code, name, description, life_months)
WHERE NOT EXISTS (
    SELECT 1 FROM asset_categories ac
    WHERE ac.tenant_id = tnt.id AND ac.code = t.code
);
