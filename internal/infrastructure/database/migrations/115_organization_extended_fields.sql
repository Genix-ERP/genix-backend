-- Add extended company/organization fields based on Uzbekistan business requirements

-- STIR - Tax payer number (can be same as INN or different)
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS stir VARCHAR(20);

-- OKED - Economic Activity Classifier Code
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS oked VARCHAR(20);

-- Bank details
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS bank_account VARCHAR(30);
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS bank_mfo VARCHAR(10);
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS bank_name VARCHAR(255);

-- VAT Payer status
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS is_vat_payer BOOLEAN DEFAULT false;

-- Tax regime: general, simplified, single
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS tax_regime VARCHAR(50);

-- Activity status: active, suspended, liquidating, dormant
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS activity_status VARCHAR(50) DEFAULT 'active';

-- Business group/cluster: construction, furniture, auto, manufacturing, etc.
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS business_group VARCHAR(100);

-- Intercompany relations (comma-separated company names for intercompany trading)
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS intercompany_relations TEXT;

-- Director/CEO information
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS director_name VARCHAR(255);
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS director_phone VARCHAR(50);

-- Legal/registered address (separate from contact address)
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS legal_address TEXT;

-- Notes/comments
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS notes TEXT;

-- Create indexes for commonly queried fields
CREATE INDEX IF NOT EXISTS idx_organizations_stir ON organizations(stir);
CREATE INDEX IF NOT EXISTS idx_organizations_oked ON organizations(oked);
CREATE INDEX IF NOT EXISTS idx_organizations_activity_status ON organizations(activity_status);
CREATE INDEX IF NOT EXISTS idx_organizations_business_group ON organizations(business_group);
CREATE INDEX IF NOT EXISTS idx_organizations_is_vat_payer ON organizations(is_vat_payer);