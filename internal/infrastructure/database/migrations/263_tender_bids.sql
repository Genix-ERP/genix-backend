-- Tender Platform: Bids
CREATE TABLE IF NOT EXISTS tender_bids (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tender_id UUID NOT NULL REFERENCES tender_tenders(id) ON DELETE CASCADE,
    supplier_id UUID NOT NULL REFERENCES tender_company_profiles(id) ON DELETE CASCADE,
    total_price DECIMAL(18,2) NOT NULL,
    currency VARCHAR(3) NOT NULL DEFAULT 'UZS',
    delivery_days INTEGER NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'rejected')),
    note TEXT,
    attachment VARCHAR(500),
    rejection_reason TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(tender_id, supplier_id)
);

CREATE INDEX IF NOT EXISTS idx_tender_bids_tender ON tender_bids(tender_id);
CREATE INDEX IF NOT EXISTS idx_tender_bids_supplier ON tender_bids(supplier_id);
CREATE INDEX IF NOT EXISTS idx_tender_bids_status ON tender_bids(status);
