-- Tender Platform: Products (Supplier catalog)
CREATE TABLE IF NOT EXISTS tender_products (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID,
    supplier_id UUID NOT NULL REFERENCES tender_company_profiles(id) ON DELETE CASCADE,
    category_id UUID REFERENCES tender_categories(id),
    name VARCHAR(255) NOT NULL,
    name_ru VARCHAR(255),
    description TEXT,
    unit VARCHAR(20) NOT NULL DEFAULT 'dona',
    price DECIMAL(18,2) NOT NULL DEFAULT 0,
    wholesale_price DECIMAL(18,2),
    wholesale_min_qty DECIMAL(12,2),
    currency VARCHAR(3) NOT NULL DEFAULT 'UZS',
    availability VARCHAR(20) NOT NULL DEFAULT 'available' CHECK (availability IN ('available', 'on_order', 'unavailable')),
    delivery_days INTEGER,
    delivery_regions UUID[],
    images TEXT[],
    certificates TEXT[],
    specs JSONB,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    view_count INTEGER DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_tender_products_supplier ON tender_products(supplier_id);
CREATE INDEX IF NOT EXISTS idx_tender_products_category ON tender_products(category_id);
CREATE INDEX IF NOT EXISTS idx_tender_products_tenant ON tender_products(tenant_id);
CREATE INDEX IF NOT EXISTS idx_tender_products_price ON tender_products(price);
CREATE INDEX IF NOT EXISTS idx_tender_products_active ON tender_products(is_active) WHERE deleted_at IS NULL;
-- Full text search index
CREATE INDEX IF NOT EXISTS idx_tender_products_search ON tender_products USING gin(to_tsvector('simple', coalesce(name, '') || ' ' || coalesce(name_ru, '') || ' ' || coalesce(description, '')));
