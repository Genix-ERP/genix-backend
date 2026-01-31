-- Migration: 016_supplier_price_history
-- Description: Create supplier price history table for tracking product prices from vendors

-- =====================================================
-- SUPPLIER PRICE HISTORY TABLE
-- =====================================================

CREATE TABLE IF NOT EXISTS supplier_price_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES products(id),
    vendor_id UUID NOT NULL REFERENCES contacts(id),
    unit_price DECIMAL(15, 4) NOT NULL,
    currency_id UUID REFERENCES currencies(id),
    effective_date DATE NOT NULL DEFAULT CURRENT_DATE,
    min_quantity DECIMAL(15, 4) DEFAULT 1,
    notes TEXT,
    source VARCHAR(50), -- 'purchase_order', 'rfq', 'manual'
    source_id UUID,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- =====================================================
-- INDEXES
-- =====================================================

CREATE INDEX IF NOT EXISTS idx_supplier_price_history_tenant ON supplier_price_history(tenant_id);
CREATE INDEX IF NOT EXISTS idx_supplier_price_history_product ON supplier_price_history(product_id, vendor_id);
CREATE INDEX IF NOT EXISTS idx_supplier_price_history_vendor ON supplier_price_history(vendor_id);
CREATE INDEX IF NOT EXISTS idx_supplier_price_history_date ON supplier_price_history(effective_date);

-- =====================================================
-- ADD PERMISSION FOR PRICE HISTORY
-- =====================================================

INSERT INTO permissions (id, module, resource, action, description)
VALUES
    (gen_random_uuid(), 'purchase', 'price_history', 'read', 'View supplier price history'),
    (gen_random_uuid(), 'purchase', 'price_history', 'create', 'Add price history records'),
    (gen_random_uuid(), 'purchase', 'price_history', 'delete', 'Delete price history records')
ON CONFLICT DO NOTHING;
