-- Tender Platform: Bid Items (price per tender item)
CREATE TABLE IF NOT EXISTS tender_bid_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    bid_id UUID NOT NULL REFERENCES tender_bids(id) ON DELETE CASCADE,
    tender_item_id UUID NOT NULL REFERENCES tender_items(id) ON DELETE CASCADE,
    unit_price DECIMAL(18,2) NOT NULL,
    total_price DECIMAL(18,2) NOT NULL,
    note TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tender_bid_items_bid ON tender_bid_items(bid_id);
CREATE INDEX IF NOT EXISTS idx_tender_bid_items_item ON tender_bid_items(tender_item_id);
