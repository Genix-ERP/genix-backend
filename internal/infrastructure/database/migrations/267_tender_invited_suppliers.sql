-- Tender Platform: Invited Suppliers (for closed tenders)
CREATE TABLE IF NOT EXISTS tender_invited_suppliers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tender_id UUID NOT NULL REFERENCES tender_tenders(id) ON DELETE CASCADE,
    supplier_id UUID NOT NULL REFERENCES tender_company_profiles(id) ON DELETE CASCADE,
    invited_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(tender_id, supplier_id)
);

CREATE INDEX IF NOT EXISTS idx_tender_invited_tender ON tender_invited_suppliers(tender_id);
CREATE INDEX IF NOT EXISTS idx_tender_invited_supplier ON tender_invited_suppliers(supplier_id);
