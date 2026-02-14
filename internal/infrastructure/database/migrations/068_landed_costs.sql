-- ============================================
-- LANDED COSTS MODULE
-- Allocate additional costs (shipping, duties, etc.) to product costs
-- ============================================

-- ============================================
-- LANDED COSTS TABLE (main header)
-- ============================================
CREATE TABLE IF NOT EXISTS landed_costs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Reference
    landed_cost_number VARCHAR(50) NOT NULL,
    goods_receipt_id UUID NOT NULL REFERENCES goods_receipts(id),
    gr_number VARCHAR(50),
    purchase_order_id UUID REFERENCES purchase_orders(id),
    po_number VARCHAR(50),

    -- Vendor info (copied from GR)
    supplier_id UUID REFERENCES contacts(id),
    supplier_name VARCHAR(255),

    -- Dates
    cost_date DATE NOT NULL DEFAULT CURRENT_DATE,

    -- Totals
    product_value DECIMAL(20, 4) DEFAULT 0,        -- Total value of products from GR
    total_landed_cost DECIMAL(20, 4) DEFAULT 0,    -- Sum of all additional costs
    total_cost DECIMAL(20, 4) DEFAULT 0,           -- product_value + total_landed_cost

    -- Status: draft, validated, cancelled
    status VARCHAR(20) DEFAULT 'draft',

    -- Allocation method: by_quantity, by_value, by_weight, by_volume, equal
    allocation_method VARCHAR(30) DEFAULT 'by_value',

    notes TEXT,

    -- Audit
    created_by UUID REFERENCES users(id),
    validated_by UUID REFERENCES users(id),
    validated_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,

    UNIQUE(tenant_id, landed_cost_number)
);

-- ============================================
-- LANDED COST TYPES (configurable cost categories)
-- ============================================
CREATE TABLE IF NOT EXISTS landed_cost_types (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    name VARCHAR(100) NOT NULL,
    code VARCHAR(20),
    description TEXT,

    -- Default allocation method for this type
    default_allocation_method VARCHAR(30) DEFAULT 'by_value',

    -- Accounting integration
    expense_account_id UUID,        -- GL account for this cost type

    is_active BOOLEAN DEFAULT true,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(tenant_id, name)
);

-- ============================================
-- LANDED COST LINES (additional costs to allocate)
-- ============================================
CREATE TABLE IF NOT EXISTS landed_cost_lines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    landed_cost_id UUID NOT NULL REFERENCES landed_costs(id) ON DELETE CASCADE,

    line_number INTEGER NOT NULL,

    -- Cost type
    cost_type_id UUID REFERENCES landed_cost_types(id),
    cost_type_name VARCHAR(100) NOT NULL,

    -- Vendor for this cost (e.g., shipping company)
    vendor_id UUID REFERENCES contacts(id),
    vendor_name VARCHAR(255),

    -- Amount
    amount DECIMAL(20, 4) NOT NULL,
    currency VARCHAR(10) DEFAULT 'UZS',

    -- Reference (invoice number, etc.)
    reference VARCHAR(100),

    -- Allocation method override (if different from header)
    allocation_method VARCHAR(30),

    notes TEXT,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(landed_cost_id, line_number)
);

-- ============================================
-- LANDED COST ALLOCATIONS (per product allocation)
-- ============================================
CREATE TABLE IF NOT EXISTS landed_cost_allocations (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    landed_cost_id UUID NOT NULL REFERENCES landed_costs(id) ON DELETE CASCADE,
    landed_cost_line_id UUID NOT NULL REFERENCES landed_cost_lines(id) ON DELETE CASCADE,

    -- Product from GR
    goods_receipt_line_id UUID REFERENCES goods_receipt_lines(id),
    product_id UUID REFERENCES products(id),
    product_name VARCHAR(255),

    -- Original values from GR
    quantity DECIMAL(20, 4) NOT NULL,
    original_unit_cost DECIMAL(20, 4) NOT NULL,
    original_total_cost DECIMAL(20, 4) NOT NULL,

    -- Allocation calculation
    allocation_base DECIMAL(20, 4),          -- Value used for allocation (qty, value, weight, etc.)
    allocation_percent DECIMAL(10, 6),       -- Percentage of this cost allocated to this product
    allocated_amount DECIMAL(20, 4) NOT NULL, -- Actual amount allocated

    -- New costs after allocation
    new_unit_cost DECIMAL(20, 4),
    new_total_cost DECIMAL(20, 4),

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- ============================================
-- INDEXES
-- ============================================
CREATE INDEX IF NOT EXISTS idx_landed_costs_tenant ON landed_costs(tenant_id);
CREATE INDEX IF NOT EXISTS idx_landed_costs_gr ON landed_costs(goods_receipt_id);
CREATE INDEX IF NOT EXISTS idx_landed_costs_po ON landed_costs(purchase_order_id);
CREATE INDEX IF NOT EXISTS idx_landed_costs_status ON landed_costs(status);
CREATE INDEX IF NOT EXISTS idx_landed_costs_date ON landed_costs(cost_date);

CREATE INDEX IF NOT EXISTS idx_landed_cost_types_tenant ON landed_cost_types(tenant_id);

CREATE INDEX IF NOT EXISTS idx_landed_cost_lines_lc ON landed_cost_lines(landed_cost_id);
CREATE INDEX IF NOT EXISTS idx_landed_cost_lines_type ON landed_cost_lines(cost_type_id);

CREATE INDEX IF NOT EXISTS idx_landed_cost_alloc_lc ON landed_cost_allocations(landed_cost_id);
CREATE INDEX IF NOT EXISTS idx_landed_cost_alloc_line ON landed_cost_allocations(landed_cost_line_id);
CREATE INDEX IF NOT EXISTS idx_landed_cost_alloc_product ON landed_cost_allocations(product_id);
CREATE INDEX IF NOT EXISTS idx_landed_cost_alloc_gr_line ON landed_cost_allocations(goods_receipt_line_id);

-- ============================================
-- DEFAULT LANDED COST TYPES
-- ============================================
-- Note: These will be inserted per tenant when needed

-- ============================================
-- TRIGGERS
-- ============================================

-- Update landed_costs.updated_at
DROP TRIGGER IF EXISTS trigger_landed_costs_updated_at ON landed_costs;
CREATE TRIGGER trigger_landed_costs_updated_at
    BEFORE UPDATE ON landed_costs
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- Update landed_cost_types.updated_at
DROP TRIGGER IF EXISTS trigger_landed_cost_types_updated_at ON landed_cost_types;
CREATE TRIGGER trigger_landed_cost_types_updated_at
    BEFORE UPDATE ON landed_cost_types
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- ============================================
-- COMMENTS
-- ============================================
COMMENT ON TABLE landed_costs IS 'Landed costs for allocating additional costs (freight, duties, etc.) to product costs';
COMMENT ON TABLE landed_cost_types IS 'Configurable types of landed costs (shipping, customs, insurance, etc.)';
COMMENT ON TABLE landed_cost_lines IS 'Individual cost items to be allocated';
COMMENT ON TABLE landed_cost_allocations IS 'Per-product allocation of landed costs';

COMMENT ON COLUMN landed_costs.allocation_method IS 'How costs are distributed: by_value (proportional to product value), by_quantity (equal per unit), by_weight, by_volume, equal (split equally)';
COMMENT ON COLUMN landed_cost_allocations.allocation_base IS 'The base value used for allocation calculation (quantity, value, weight, etc.)';
COMMENT ON COLUMN landed_cost_allocations.allocated_amount IS 'The portion of landed cost allocated to this product';
