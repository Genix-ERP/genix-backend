-- Add company-specific fields to organizations table for multi-company support

-- Add country column
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS country VARCHAR(100);

-- Add currency column (3-letter ISO code like UZS, USD, EUR)
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS currency VARCHAR(10);

-- Add accounting standard column
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS accounting_standard VARCHAR(50);

-- Add logo URL column
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS logo_url TEXT;

-- Add settings column to store organization-specific settings as JSON
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS settings JSONB DEFAULT '{}';

-- Create index for faster lookups by currency
CREATE INDEX IF NOT EXISTS idx_organizations_currency ON organizations(currency);

-- Create index for faster lookups by country
CREATE INDEX IF NOT EXISTS idx_organizations_country ON organizations(country);
