-- Tender Platform: Tenders
CREATE TABLE IF NOT EXISTS tender_tenders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID,
    buyer_id UUID NOT NULL REFERENCES tender_company_profiles(id) ON DELETE CASCADE,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    status VARCHAR(20) NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'active', 'expired', 'completed', 'cancelled')),
    tender_type VARCHAR(10) NOT NULL DEFAULT 'open' CHECK (tender_type IN ('open', 'closed')),
    region_id UUID REFERENCES tender_regions(id),
    delivery_address TEXT,
    deadline TIMESTAMP WITH TIME ZONE NOT NULL,
    delivery_date DATE,
    currency VARCHAR(3) NOT NULL DEFAULT 'UZS',
    attachment VARCHAR(500),
    bid_count INTEGER DEFAULT 0,
    selected_bid_id UUID,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_tender_tenders_buyer ON tender_tenders(buyer_id);
CREATE INDEX IF NOT EXISTS idx_tender_tenders_status ON tender_tenders(status);
CREATE INDEX IF NOT EXISTS idx_tender_tenders_region ON tender_tenders(region_id);
CREATE INDEX IF NOT EXISTS idx_tender_tenders_deadline ON tender_tenders(deadline);
CREATE INDEX IF NOT EXISTS idx_tender_tenders_tenant ON tender_tenders(tenant_id);
CREATE INDEX IF NOT EXISTS idx_tender_tenders_created ON tender_tenders(created_at DESC);
