-- Tender Platform: Reviews and Ratings
CREATE TABLE IF NOT EXISTS tender_reviews (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tender_id UUID REFERENCES tender_tenders(id) ON DELETE SET NULL,
    reviewer_id UUID NOT NULL REFERENCES tender_company_profiles(id) ON DELETE CASCADE,
    supplier_id UUID NOT NULL REFERENCES tender_company_profiles(id) ON DELETE CASCADE,
    quality_rating INTEGER NOT NULL CHECK (quality_rating BETWEEN 1 AND 5),
    price_rating INTEGER NOT NULL CHECK (price_rating BETWEEN 1 AND 5),
    delivery_rating INTEGER NOT NULL CHECK (delivery_rating BETWEEN 1 AND 5),
    communication_rating INTEGER NOT NULL CHECK (communication_rating BETWEEN 1 AND 5),
    overall_rating DECIMAL(3,2) NOT NULL,
    comment TEXT,
    is_visible BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(tender_id, reviewer_id)
);

CREATE INDEX IF NOT EXISTS idx_tender_reviews_supplier ON tender_reviews(supplier_id);
CREATE INDEX IF NOT EXISTS idx_tender_reviews_reviewer ON tender_reviews(reviewer_id);
