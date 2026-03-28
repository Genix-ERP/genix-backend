-- Tender Platform: Product Categories (3-level hierarchy)
CREATE TABLE IF NOT EXISTS tender_categories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID,
    parent_id UUID REFERENCES tender_categories(id) ON DELETE SET NULL,
    name VARCHAR(255) NOT NULL,
    name_ru VARCHAR(255),
    slug VARCHAR(255) NOT NULL,
    icon VARCHAR(100),
    banner VARCHAR(500),
    level INTEGER NOT NULL DEFAULT 0,
    sort_order INTEGER NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(slug, tenant_id)
);

CREATE INDEX IF NOT EXISTS idx_tender_categories_parent ON tender_categories(parent_id);
CREATE INDEX IF NOT EXISTS idx_tender_categories_tenant ON tender_categories(tenant_id);
CREATE INDEX IF NOT EXISTS idx_tender_categories_slug ON tender_categories(slug);

-- Seed base categories
INSERT INTO tender_categories (name, name_ru, slug, level, sort_order) VALUES
    ('Qurilish materiallari', 'Строительные материалы', 'qurilish-materiallari', 0, 1),
    ('Qurilish texnikasi', 'Строительная техника', 'qurilish-texnikasi', 0, 2),
    ('Elektr jihozlari', 'Электрооборудование', 'elektr-jihozlari', 0, 3),
    ('Sanitariya-texnik', 'Сантехника', 'sanitariya-texnik', 0, 4),
    ('Bezak materiallari', 'Отделочные материалы', 'bezak-materiallari', 0, 5),
    ('Metall konstruksiyalar', 'Металлоконструкции', 'metall-konstruksiyalar', 0, 6),
    ('Yog''och materiallari', 'Пиломатериалы', 'yogoch-materiallari', 0, 7),
    ('Himoya vositalari', 'Средства защиты', 'himoya-vositalari', 0, 8)
ON CONFLICT (slug, tenant_id) DO NOTHING;
