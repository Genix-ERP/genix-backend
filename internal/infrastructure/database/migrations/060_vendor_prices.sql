-- Migration: Vendor Prices
-- Stores product prices from different vendors for replenishment calculations

CREATE TABLE IF NOT EXISTS vendor_prices (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    vendor_id UUID NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    price DECIMAL(20, 4) NOT NULL,
    currency VARCHAR(3) DEFAULT 'USD',
    min_quantity DECIMAL(20, 4) DEFAULT 1,
    lead_time_days INTEGER DEFAULT 0,
    notes TEXT,
    valid_from DATE,
    valid_until DATE,
    is_active BOOLEAN DEFAULT true,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

-- Indexes for fast lookup
CREATE INDEX IF NOT EXISTS idx_vendor_prices_tenant ON vendor_prices(tenant_id);
CREATE INDEX IF NOT EXISTS idx_vendor_prices_vendor ON vendor_prices(vendor_id);
CREATE INDEX IF NOT EXISTS idx_vendor_prices_product ON vendor_prices(product_id);
CREATE INDEX IF NOT EXISTS idx_vendor_prices_vendor_product ON vendor_prices(vendor_id, product_id);
CREATE INDEX IF NOT EXISTS idx_vendor_prices_active ON vendor_prices(is_active) WHERE is_active = true;

-- Unique constraint to prevent duplicate active prices for same vendor-product combination
CREATE UNIQUE INDEX IF NOT EXISTS idx_vendor_prices_unique_active
ON vendor_prices(tenant_id, vendor_id, product_id)
WHERE deleted_at IS NULL AND is_active = true;
