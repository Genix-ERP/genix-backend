-- Tender Platform: Company Profiles (extends users for tender platform)
CREATE TABLE IF NOT EXISTS tender_company_profiles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id UUID,
    role VARCHAR(20) NOT NULL DEFAULT 'buyer' CHECK (role IN ('buyer', 'supplier', 'admin')),
    company_name VARCHAR(255) NOT NULL,
    inn VARCHAR(20) NOT NULL,
    phone VARCHAR(20) NOT NULL,
    region_id UUID REFERENCES tender_regions(id),
    address TEXT,
    logo VARCHAR(500),
    banner VARCHAR(500),
    description TEXT,
    website VARCHAR(255),
    activity_areas TEXT[],
    license_number VARCHAR(100),
    license_file VARCHAR(500),
    is_verified BOOLEAN NOT NULL DEFAULT FALSE,
    verified_at TIMESTAMP WITH TIME ZONE,
    rating DECIMAL(3,2) DEFAULT 0,
    review_count INTEGER DEFAULT 0,
    tender_count INTEGER DEFAULT 0,
    bid_count INTEGER DEFAULT 0,
    won_count INTEGER DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_tender_company_user ON tender_company_profiles(user_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tender_company_tenant ON tender_company_profiles(tenant_id);
CREATE INDEX IF NOT EXISTS idx_tender_company_region ON tender_company_profiles(region_id);
CREATE INDEX IF NOT EXISTS idx_tender_company_role ON tender_company_profiles(role);
CREATE INDEX IF NOT EXISTS idx_tender_company_inn ON tender_company_profiles(inn);
CREATE INDEX IF NOT EXISTS idx_tender_company_verified ON tender_company_profiles(is_verified);
