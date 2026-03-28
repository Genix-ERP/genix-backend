-- Tender Platform: Tender Items (product line items)
CREATE TABLE IF NOT EXISTS tender_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tender_id UUID NOT NULL REFERENCES tender_tenders(id) ON DELETE CASCADE,
    category_id UUID REFERENCES tender_categories(id),
    name VARCHAR(255) NOT NULL,
    quantity DECIMAL(12,2) NOT NULL,
    unit VARCHAR(20) NOT NULL DEFAULT 'dona',
    specs TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tender_items_tender ON tender_items(tender_id);
