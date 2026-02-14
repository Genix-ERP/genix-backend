-- Migration: Blanket Orders (Standing Orders / Call-off Contracts)
-- Blanket orders are long-term purchase agreements with vendors for a fixed quantity/amount
-- with releases (call-offs) made against them over time

-- ============================================
-- BLANKET ORDERS (Master Agreement)
-- ============================================
CREATE TABLE IF NOT EXISTS blanket_orders (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Basic info
    blanket_number VARCHAR(50) NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT,

    -- Vendor
    vendor_id UUID REFERENCES contacts(id) ON DELETE SET NULL,
    vendor_name VARCHAR(255),

    -- Agreement period
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,

    -- Agreement type: quantity-based or value-based
    agreement_type VARCHAR(20) NOT NULL DEFAULT 'quantity', -- 'quantity' or 'value'

    -- For value-based agreements
    total_value DECIMAL(20, 4) DEFAULT 0,
    released_value DECIMAL(20, 4) DEFAULT 0,
    remaining_value DECIMAL(20, 4) GENERATED ALWAYS AS (total_value - released_value) STORED,

    -- Currency and terms
    currency VARCHAR(10) DEFAULT 'UZS',
    payment_terms VARCHAR(100),
    delivery_terms VARCHAR(100),
    incoterms VARCHAR(20),

    -- Warehouse
    warehouse_id UUID REFERENCES warehouses(id),

    -- Status: draft, active, on_hold, completed, expired, cancelled
    status VARCHAR(20) DEFAULT 'draft',

    -- Approval
    requires_approval BOOLEAN DEFAULT false,
    approved_by UUID REFERENCES users(id),
    approved_at TIMESTAMP WITH TIME ZONE,

    -- Terms and conditions
    terms_conditions TEXT,
    special_instructions TEXT,

    -- Scheduling
    min_release_value DECIMAL(20, 4),        -- Minimum value per release
    max_release_value DECIMAL(20, 4),        -- Maximum value per release
    release_frequency VARCHAR(20),           -- weekly, monthly, quarterly, as_needed
    auto_release BOOLEAN DEFAULT false,      -- Auto-create releases based on schedule
    next_release_date DATE,

    -- Pricing
    price_adjustment_allowed BOOLEAN DEFAULT false,
    max_price_increase_percent DECIMAL(5, 2),

    -- Notifications
    expiry_alert_days INTEGER DEFAULT 30,
    low_quantity_alert_percent DECIMAL(5, 2) DEFAULT 20,

    notes TEXT,
    attachments JSONB DEFAULT '[]',

    -- Audit
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,

    UNIQUE(tenant_id, blanket_number)
);

-- ============================================
-- BLANKET ORDER LINES (Products/Items in Agreement)
-- ============================================
CREATE TABLE IF NOT EXISTS blanket_order_lines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    blanket_order_id UUID NOT NULL REFERENCES blanket_orders(id) ON DELETE CASCADE,
    line_number INTEGER NOT NULL,

    -- Product
    product_id UUID REFERENCES products(id) ON DELETE SET NULL,
    product_name VARCHAR(255) NOT NULL,
    product_code VARCHAR(100),
    description TEXT,

    -- Quantities
    agreed_quantity DECIMAL(20, 4) NOT NULL,      -- Total agreed quantity
    released_quantity DECIMAL(20, 4) DEFAULT 0,   -- Already released/called-off
    received_quantity DECIMAL(20, 4) DEFAULT 0,   -- Actually received
    remaining_quantity DECIMAL(20, 4) GENERATED ALWAYS AS (agreed_quantity - released_quantity) STORED,

    -- Unit and pricing
    unit_id UUID REFERENCES units_of_measure(id),
    unit_name VARCHAR(50),
    unit_price DECIMAL(20, 4) NOT NULL,
    discount_percent DECIMAL(5, 2) DEFAULT 0,
    tax_id UUID REFERENCES tax_rates(id),

    -- Computed line value
    line_value DECIMAL(20, 4) GENERATED ALWAYS AS (agreed_quantity * unit_price * (1 - discount_percent / 100)) STORED,

    -- Constraints
    min_release_qty DECIMAL(20, 4),              -- Minimum quantity per release
    max_release_qty DECIMAL(20, 4),              -- Maximum quantity per release
    lead_time_days INTEGER DEFAULT 0,

    -- Warehouse
    warehouse_id UUID REFERENCES warehouses(id),

    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(blanket_order_id, line_number)
);

-- ============================================
-- BLANKET ORDER RELEASES (Call-offs / POs against blanket)
-- ============================================
CREATE TABLE IF NOT EXISTS blanket_order_releases (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Link to blanket order
    blanket_order_id UUID NOT NULL REFERENCES blanket_orders(id) ON DELETE CASCADE,

    -- Release info
    release_number VARCHAR(50) NOT NULL,
    release_date DATE NOT NULL,
    expected_date DATE,

    -- Link to generated PO (if any)
    purchase_order_id UUID REFERENCES purchase_orders(id) ON DELETE SET NULL,

    -- Status: draft, confirmed, shipped, received, cancelled
    status VARCHAR(20) DEFAULT 'draft',

    -- Totals
    subtotal DECIMAL(20, 4) DEFAULT 0,
    discount_amount DECIMAL(20, 4) DEFAULT 0,
    tax_amount DECIMAL(20, 4) DEFAULT 0,
    total_amount DECIMAL(20, 4) DEFAULT 0,

    -- Shipping
    shipping_address JSONB,
    shipping_method VARCHAR(100),
    tracking_number VARCHAR(100),

    notes TEXT,

    -- Audit
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(tenant_id, release_number)
);

-- ============================================
-- BLANKET ORDER RELEASE LINES
-- ============================================
CREATE TABLE IF NOT EXISTS blanket_order_release_lines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    release_id UUID NOT NULL REFERENCES blanket_order_releases(id) ON DELETE CASCADE,
    blanket_line_id UUID NOT NULL REFERENCES blanket_order_lines(id) ON DELETE CASCADE,
    line_number INTEGER NOT NULL,

    -- Product (denormalized for convenience)
    product_id UUID REFERENCES products(id),
    product_name VARCHAR(255),

    -- Quantities
    quantity DECIMAL(20, 4) NOT NULL,
    quantity_received DECIMAL(20, 4) DEFAULT 0,

    -- Pricing (from blanket line, can be overridden if allowed)
    unit_price DECIMAL(20, 4) NOT NULL,
    discount_percent DECIMAL(5, 2) DEFAULT 0,
    tax_amount DECIMAL(20, 4) DEFAULT 0,
    line_total DECIMAL(20, 4) GENERATED ALWAYS AS (quantity * unit_price * (1 - discount_percent / 100)) STORED,

    -- Warehouse
    warehouse_id UUID REFERENCES warehouses(id),

    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(release_id, line_number)
);

-- ============================================
-- INDEXES
-- ============================================
CREATE INDEX IF NOT EXISTS idx_blanket_orders_tenant ON blanket_orders(tenant_id);
CREATE INDEX IF NOT EXISTS idx_blanket_orders_vendor ON blanket_orders(vendor_id);
CREATE INDEX IF NOT EXISTS idx_blanket_orders_status ON blanket_orders(tenant_id, status);
CREATE INDEX IF NOT EXISTS idx_blanket_orders_dates ON blanket_orders(start_date, end_date);
CREATE INDEX IF NOT EXISTS idx_blanket_orders_number ON blanket_orders(blanket_number);

CREATE INDEX IF NOT EXISTS idx_blanket_order_lines_order ON blanket_order_lines(blanket_order_id);
CREATE INDEX IF NOT EXISTS idx_blanket_order_lines_product ON blanket_order_lines(product_id);

CREATE INDEX IF NOT EXISTS idx_blanket_releases_tenant ON blanket_order_releases(tenant_id);
CREATE INDEX IF NOT EXISTS idx_blanket_releases_blanket ON blanket_order_releases(blanket_order_id);
CREATE INDEX IF NOT EXISTS idx_blanket_releases_status ON blanket_order_releases(status);
CREATE INDEX IF NOT EXISTS idx_blanket_releases_po ON blanket_order_releases(purchase_order_id);

CREATE INDEX IF NOT EXISTS idx_blanket_release_lines_release ON blanket_order_release_lines(release_id);
CREATE INDEX IF NOT EXISTS idx_blanket_release_lines_blanket_line ON blanket_order_release_lines(blanket_line_id);

-- ============================================
-- TRIGGERS
-- ============================================

-- Update blanket_orders.updated_at
CREATE OR REPLACE FUNCTION update_blanket_orders_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trigger_blanket_orders_updated_at ON blanket_orders;
CREATE TRIGGER trigger_blanket_orders_updated_at
    BEFORE UPDATE ON blanket_orders
    FOR EACH ROW
    EXECUTE FUNCTION update_blanket_orders_updated_at();

-- Update blanket_order_lines.updated_at
DROP TRIGGER IF EXISTS trigger_blanket_order_lines_updated_at ON blanket_order_lines;
CREATE TRIGGER trigger_blanket_order_lines_updated_at
    BEFORE UPDATE ON blanket_order_lines
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- Update blanket_order_releases.updated_at
DROP TRIGGER IF EXISTS trigger_blanket_order_releases_updated_at ON blanket_order_releases;
CREATE TRIGGER trigger_blanket_order_releases_updated_at
    BEFORE UPDATE ON blanket_order_releases
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- ============================================
-- Update released quantities when release is confirmed
-- ============================================
CREATE OR REPLACE FUNCTION update_blanket_released_quantities()
RETURNS TRIGGER AS $$
BEGIN
    -- When a release is confirmed, update the blanket order line quantities
    IF NEW.status = 'confirmed' AND (OLD.status IS NULL OR OLD.status != 'confirmed') THEN
        -- Update each blanket order line's released_quantity
        UPDATE blanket_order_lines bol
        SET released_quantity = released_quantity + borl.quantity,
            updated_at = CURRENT_TIMESTAMP
        FROM blanket_order_release_lines borl
        WHERE borl.release_id = NEW.id
          AND borl.blanket_line_id = bol.id;

        -- Update blanket order's released_value
        UPDATE blanket_orders
        SET released_value = released_value + NEW.total_amount,
            updated_at = CURRENT_TIMESTAMP
        WHERE id = NEW.blanket_order_id;
    END IF;

    -- When a release is cancelled after being confirmed, reverse the quantities
    IF NEW.status = 'cancelled' AND OLD.status = 'confirmed' THEN
        UPDATE blanket_order_lines bol
        SET released_quantity = released_quantity - borl.quantity,
            updated_at = CURRENT_TIMESTAMP
        FROM blanket_order_release_lines borl
        WHERE borl.release_id = NEW.id
          AND borl.blanket_line_id = bol.id;

        UPDATE blanket_orders
        SET released_value = released_value - OLD.total_amount,
            updated_at = CURRENT_TIMESTAMP
        WHERE id = NEW.blanket_order_id;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trigger_update_blanket_released ON blanket_order_releases;
CREATE TRIGGER trigger_update_blanket_released
    AFTER UPDATE ON blanket_order_releases
    FOR EACH ROW
    EXECUTE FUNCTION update_blanket_released_quantities();

-- ============================================
-- Link purchase_orders to blanket_order_releases
-- ============================================
ALTER TABLE purchase_orders
ADD COLUMN IF NOT EXISTS blanket_release_id UUID REFERENCES blanket_order_releases(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_purchase_orders_blanket_release ON purchase_orders(blanket_release_id) WHERE blanket_release_id IS NOT NULL;

COMMENT ON COLUMN purchase_orders.blanket_release_id IS 'Reference to the blanket order release that generated this PO';

-- ============================================
-- COMMENTS
-- ============================================
COMMENT ON TABLE blanket_orders IS 'Blanket orders (standing orders/call-off contracts) - long-term purchase agreements';
COMMENT ON TABLE blanket_order_lines IS 'Products/items included in the blanket order agreement';
COMMENT ON TABLE blanket_order_releases IS 'Releases (call-offs) made against blanket orders';
COMMENT ON TABLE blanket_order_release_lines IS 'Line items for each release';

COMMENT ON COLUMN blanket_orders.agreement_type IS 'Type of agreement: quantity (fixed quantities) or value (fixed total value)';
COMMENT ON COLUMN blanket_orders.release_frequency IS 'Expected frequency of releases: weekly, monthly, quarterly, as_needed';
COMMENT ON COLUMN blanket_order_lines.remaining_quantity IS 'Auto-computed: agreed_quantity - released_quantity';
COMMENT ON COLUMN blanket_order_releases.purchase_order_id IS 'Link to PO created from this release';
