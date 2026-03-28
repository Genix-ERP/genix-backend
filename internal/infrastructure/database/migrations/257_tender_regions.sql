-- Tender Platform: Regions
CREATE TABLE IF NOT EXISTS tender_regions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL,
    code VARCHAR(50) NOT NULL UNIQUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Seed Uzbekistan regions
INSERT INTO tender_regions (name, code) VALUES
    ('Toshkent shahri', 'tashkent_city'),
    ('Toshkent viloyati', 'tashkent_region'),
    ('Andijon', 'andijan'),
    ('Farg''ona', 'fergana'),
    ('Samarqand', 'samarkand'),
    ('Buxoro', 'bukhara'),
    ('Navoiy', 'navoi'),
    ('Namangan', 'namangan'),
    ('Qashqadaryo', 'kashkadarya'),
    ('Surxondaryo', 'surkhandarya'),
    ('Jizzax', 'jizzakh'),
    ('Sirdaryo', 'syrdarya'),
    ('Xorazm', 'khorezm'),
    ('Qoraqalpog''iston', 'karakalpakstan')
ON CONFLICT (code) DO NOTHING;
