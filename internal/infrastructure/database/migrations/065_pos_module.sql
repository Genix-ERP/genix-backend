-- Migration: Point of Sale (POS) Module
-- Enables retail sales with quick product selection, session management, and receipt printing

-- ============================================
-- POS CONFIGURATION
-- ============================================
CREATE TABLE IF NOT EXISTS pos_configs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,

    -- Location
    warehouse_id UUID REFERENCES warehouses(id),

    -- Receipt settings
    receipt_header TEXT,
    receipt_footer TEXT,

    -- Payment methods (JSON array of allowed payment method IDs)
    allowed_payment_methods JSONB DEFAULT '[]',
    default_payment_method_id UUID REFERENCES payment_methods(id),

    -- Behavior
    is_active BOOLEAN DEFAULT true,
    allow_discount BOOLEAN DEFAULT true,
    max_discount_percent DECIMAL(5,2) DEFAULT 100,
    require_customer BOOLEAN DEFAULT false,

    -- Tax settings
    default_tax_rate DECIMAL(5,2) DEFAULT 12,
    prices_include_tax BOOLEAN DEFAULT false,

    -- Inventory
    update_stock BOOLEAN DEFAULT true,

    -- UI settings
    show_product_images BOOLEAN DEFAULT true,
    products_per_page INTEGER DEFAULT 20,

    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,

    UNIQUE(tenant_id, name)
);

-- ============================================
-- POS SESSIONS (Shift Management)
-- ============================================
CREATE TABLE IF NOT EXISTS pos_sessions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    pos_config_id UUID NOT NULL REFERENCES pos_configs(id) ON DELETE CASCADE,

    session_number VARCHAR(50) NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id),
    user_name VARCHAR(255),

    -- Session timing
    opened_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    closed_at TIMESTAMP WITH TIME ZONE,

    -- Cash control
    opening_balance DECIMAL(20,4) DEFAULT 0,
    closing_balance DECIMAL(20,4),
    expected_balance DECIMAL(20,4),
    difference DECIMAL(20,4),

    -- Totals (computed on close)
    total_sales DECIMAL(20,4) DEFAULT 0,
    total_refunds DECIMAL(20,4) DEFAULT 0,
    total_cash_payments DECIMAL(20,4) DEFAULT 0,
    total_card_payments DECIMAL(20,4) DEFAULT 0,
    total_other_payments DECIMAL(20,4) DEFAULT 0,
    order_count INTEGER DEFAULT 0,

    -- Status: open, closing, closed
    status VARCHAR(20) DEFAULT 'open',

    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(tenant_id, session_number)
);

-- ============================================
-- POS ORDERS (Quick Sales)
-- ============================================
CREATE TABLE IF NOT EXISTS pos_orders (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    session_id UUID NOT NULL REFERENCES pos_sessions(id) ON DELETE CASCADE,

    order_number VARCHAR(50) NOT NULL,
    order_date TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    -- Customer (optional)
    customer_id UUID REFERENCES contacts(id),
    customer_name VARCHAR(255),
    customer_phone VARCHAR(50),

    -- Totals
    subtotal DECIMAL(20,4) DEFAULT 0,
    discount_percent DECIMAL(5,2) DEFAULT 0,
    discount_amount DECIMAL(20,4) DEFAULT 0,
    tax_amount DECIMAL(20,4) DEFAULT 0,
    total_amount DECIMAL(20,4) DEFAULT 0,

    -- Payment
    amount_paid DECIMAL(20,4) DEFAULT 0,
    amount_return DECIMAL(20,4) DEFAULT 0,

    -- Status: draft, paid, refunded, cancelled
    status VARCHAR(20) DEFAULT 'draft',

    -- Warehouse for stock update
    warehouse_id UUID REFERENCES warehouses(id),

    -- Link to sales order/invoice (if generated)
    sales_order_id UUID REFERENCES sales_orders(id),
    sales_invoice_id UUID REFERENCES sales_invoices(id),

    notes TEXT,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(tenant_id, order_number)
);

-- ============================================
-- POS ORDER LINES
-- ============================================
CREATE TABLE IF NOT EXISTS pos_order_lines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    order_id UUID NOT NULL REFERENCES pos_orders(id) ON DELETE CASCADE,
    line_number INTEGER NOT NULL,

    product_id UUID NOT NULL REFERENCES products(id),
    product_name VARCHAR(255) NOT NULL,
    product_code VARCHAR(100),
    barcode VARCHAR(100),

    quantity DECIMAL(20,4) NOT NULL,
    unit_price DECIMAL(20,4) NOT NULL,
    discount_percent DECIMAL(5,2) DEFAULT 0,
    discount_amount DECIMAL(20,4) DEFAULT 0,
    tax_percent DECIMAL(5,2) DEFAULT 0,
    tax_amount DECIMAL(20,4) DEFAULT 0,
    line_total DECIMAL(20,4) NOT NULL,

    -- Cost for profit calculation
    unit_cost DECIMAL(20,4) DEFAULT 0,

    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(order_id, line_number)
);

-- ============================================
-- POS PAYMENTS (Multiple Payments per Order)
-- ============================================
CREATE TABLE IF NOT EXISTS pos_payments (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    order_id UUID NOT NULL REFERENCES pos_orders(id) ON DELETE CASCADE,
    session_id UUID NOT NULL REFERENCES pos_sessions(id),

    payment_method_id UUID REFERENCES payment_methods(id),
    payment_method_name VARCHAR(100) NOT NULL,
    payment_type VARCHAR(20) NOT NULL, -- cash, card, mobile, other

    amount DECIMAL(20,4) NOT NULL,

    -- For card/mobile payments
    transaction_id VARCHAR(100),
    card_last_four VARCHAR(4),

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- ============================================
-- POS PRODUCT CATEGORIES (Quick Access)
-- ============================================
CREATE TABLE IF NOT EXISTS pos_product_categories (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    pos_config_id UUID REFERENCES pos_configs(id) ON DELETE CASCADE,

    name VARCHAR(100) NOT NULL,
    color VARCHAR(20) DEFAULT '#3B82F6',
    icon VARCHAR(50),
    display_order INTEGER DEFAULT 0,

    -- Link to product category
    product_category_id UUID REFERENCES product_categories(id),

    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(tenant_id, pos_config_id, name)
);

-- ============================================
-- POS CASH MOVEMENTS (In/Out during session)
-- ============================================
CREATE TABLE IF NOT EXISTS pos_cash_movements (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id UUID NOT NULL REFERENCES pos_sessions(id) ON DELETE CASCADE,

    movement_type VARCHAR(20) NOT NULL, -- in, out
    amount DECIMAL(20,4) NOT NULL,
    reason VARCHAR(255) NOT NULL,

    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- ============================================
-- INDEXES
-- ============================================
CREATE INDEX IF NOT EXISTS idx_pos_configs_tenant ON pos_configs(tenant_id);
CREATE INDEX IF NOT EXISTS idx_pos_configs_active ON pos_configs(tenant_id, is_active);

CREATE INDEX IF NOT EXISTS idx_pos_sessions_tenant ON pos_sessions(tenant_id);
CREATE INDEX IF NOT EXISTS idx_pos_sessions_config ON pos_sessions(pos_config_id);
CREATE INDEX IF NOT EXISTS idx_pos_sessions_status ON pos_sessions(status);
CREATE INDEX IF NOT EXISTS idx_pos_sessions_user ON pos_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_pos_sessions_date ON pos_sessions(opened_at);

CREATE INDEX IF NOT EXISTS idx_pos_orders_tenant ON pos_orders(tenant_id);
CREATE INDEX IF NOT EXISTS idx_pos_orders_session ON pos_orders(session_id);
CREATE INDEX IF NOT EXISTS idx_pos_orders_status ON pos_orders(status);
CREATE INDEX IF NOT EXISTS idx_pos_orders_date ON pos_orders(order_date);
CREATE INDEX IF NOT EXISTS idx_pos_orders_customer ON pos_orders(customer_id);

CREATE INDEX IF NOT EXISTS idx_pos_order_lines_order ON pos_order_lines(order_id);
CREATE INDEX IF NOT EXISTS idx_pos_order_lines_product ON pos_order_lines(product_id);

CREATE INDEX IF NOT EXISTS idx_pos_payments_order ON pos_payments(order_id);
CREATE INDEX IF NOT EXISTS idx_pos_payments_session ON pos_payments(session_id);

CREATE INDEX IF NOT EXISTS idx_pos_categories_tenant ON pos_product_categories(tenant_id);
CREATE INDEX IF NOT EXISTS idx_pos_categories_config ON pos_product_categories(pos_config_id);

CREATE INDEX IF NOT EXISTS idx_pos_cash_movements_session ON pos_cash_movements(session_id);

-- ============================================
-- TRIGGERS
-- ============================================

-- Update pos_configs.updated_at
DROP TRIGGER IF EXISTS trigger_pos_configs_updated_at ON pos_configs;
CREATE TRIGGER trigger_pos_configs_updated_at
    BEFORE UPDATE ON pos_configs
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- Update pos_sessions.updated_at
DROP TRIGGER IF EXISTS trigger_pos_sessions_updated_at ON pos_sessions;
CREATE TRIGGER trigger_pos_sessions_updated_at
    BEFORE UPDATE ON pos_sessions
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- Update pos_orders.updated_at
DROP TRIGGER IF EXISTS trigger_pos_orders_updated_at ON pos_orders;
CREATE TRIGGER trigger_pos_orders_updated_at
    BEFORE UPDATE ON pos_orders
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- ============================================
-- COMMENTS
-- ============================================
COMMENT ON TABLE pos_configs IS 'POS terminal configurations';
COMMENT ON TABLE pos_sessions IS 'POS sessions (shifts) for cash control';
COMMENT ON TABLE pos_orders IS 'POS sales orders (quick retail sales)';
COMMENT ON TABLE pos_order_lines IS 'Line items for POS orders';
COMMENT ON TABLE pos_payments IS 'Payments received for POS orders';
COMMENT ON TABLE pos_product_categories IS 'Quick access product categories for POS';
COMMENT ON TABLE pos_cash_movements IS 'Cash in/out movements during POS session';

COMMENT ON COLUMN pos_sessions.expected_balance IS 'Opening balance + cash sales - cash refunds + cash in - cash out';
COMMENT ON COLUMN pos_sessions.difference IS 'Closing balance - Expected balance (cash over/short)';
COMMENT ON COLUMN pos_orders.amount_return IS 'Change given to customer';
COMMENT ON COLUMN pos_order_lines.unit_cost IS 'Product cost at time of sale for profit reporting';
