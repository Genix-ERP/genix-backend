-- Tender platform users (separate from ERP users, no tenant/org required)
CREATE TABLE IF NOT EXISTS tender_users (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    email VARCHAR(255) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    full_name VARCHAR(200) NOT NULL,
    phone VARCHAR(50),
    role VARCHAR(20) NOT NULL DEFAULT 'buyer' CHECK (role IN ('buyer', 'supplier', 'admin')),
    company_name VARCHAR(255),
    inn VARCHAR(20),
    region_id UUID REFERENCES tender_regions(id),
    is_active BOOLEAN DEFAULT true,
    is_verified BOOLEAN DEFAULT false,
    last_login_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_tender_users_email ON tender_users(email);
CREATE INDEX idx_tender_users_role ON tender_users(role);
CREATE INDEX idx_tender_users_region ON tender_users(region_id);
