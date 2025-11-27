-- GenixERP ERP Modules Schema Migration
-- Finance, Inventory, Sales, Purchase, HR, Manufacturing modules

-- ============================================
-- CONTACTS (Customers, Vendors, Partners)
-- ============================================
CREATE TABLE contacts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    type VARCHAR(20) NOT NULL, -- customer, vendor, both, partner
    code VARCHAR(50) NOT NULL,
    name VARCHAR(255) NOT NULL,
    legal_name VARCHAR(255),
    tax_id VARCHAR(50),
    registration_number VARCHAR(100),
    industry VARCHAR(100),
    website VARCHAR(255),
    email VARCHAR(255),
    phone VARCHAR(50),
    fax VARCHAR(50),
    billing_address JSONB,
    shipping_address JSONB,
    payment_terms INTEGER DEFAULT 30,
    credit_limit DECIMAL(20, 4) DEFAULT 0,
    current_balance DECIMAL(20, 4) DEFAULT 0,
    currency_id UUID REFERENCES currencies(id),
    tax_exempt BOOLEAN DEFAULT false,
    tags JSONB DEFAULT '[]',
    notes TEXT,
    custom_fields JSONB DEFAULT '{}',
    is_active BOOLEAN DEFAULT true,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, code)
);

CREATE INDEX idx_contacts_tenant ON contacts(tenant_id);
CREATE INDEX idx_contacts_type ON contacts(tenant_id, type);
CREATE INDEX idx_contacts_name ON contacts USING gin(name gin_trgm_ops);

CREATE TABLE contact_persons (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    contact_id UUID NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    first_name VARCHAR(100) NOT NULL,
    last_name VARCHAR(100) NOT NULL,
    title VARCHAR(100),
    email VARCHAR(255),
    phone VARCHAR(50),
    mobile VARCHAR(50),
    is_primary BOOLEAN DEFAULT false,
    department VARCHAR(100),
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_contact_persons_contact ON contact_persons(contact_id);

-- ============================================
-- CHART OF ACCOUNTS
-- ============================================
CREATE TABLE account_types (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    code VARCHAR(20) UNIQUE NOT NULL,
    name VARCHAR(100) NOT NULL,
    category VARCHAR(50) NOT NULL, -- asset, liability, equity, revenue, expense
    normal_balance VARCHAR(10) NOT NULL, -- debit, credit
    is_system BOOLEAN DEFAULT true
);

INSERT INTO account_types (code, name, category, normal_balance) VALUES
('CASH', 'Cash and Cash Equivalents', 'asset', 'debit'),
('AR', 'Accounts Receivable', 'asset', 'debit'),
('INV', 'Inventory', 'asset', 'debit'),
('FA', 'Fixed Assets', 'asset', 'debit'),
('OA', 'Other Assets', 'asset', 'debit'),
('AP', 'Accounts Payable', 'liability', 'credit'),
('ST_LIAB', 'Short-term Liabilities', 'liability', 'credit'),
('LT_LIAB', 'Long-term Liabilities', 'liability', 'credit'),
('EQUITY', 'Equity', 'equity', 'credit'),
('RETAIN', 'Retained Earnings', 'equity', 'credit'),
('REVENUE', 'Revenue', 'revenue', 'credit'),
('COGS', 'Cost of Goods Sold', 'expense', 'debit'),
('OPEX', 'Operating Expenses', 'expense', 'debit'),
('OTHER_INC', 'Other Income', 'revenue', 'credit'),
('OTHER_EXP', 'Other Expenses', 'expense', 'debit');

CREATE TABLE accounts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    parent_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
    account_type_id UUID NOT NULL REFERENCES account_types(id),
    code VARCHAR(50) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    currency_id UUID REFERENCES currencies(id),
    is_bank_account BOOLEAN DEFAULT false,
    bank_details JSONB,
    is_control_account BOOLEAN DEFAULT false,
    is_reconcilable BOOLEAN DEFAULT false,
    current_balance DECIMAL(20, 4) DEFAULT 0,
    opening_balance DECIMAL(20, 4) DEFAULT 0,
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, organization_id, code)
);

CREATE INDEX idx_accounts_tenant ON accounts(tenant_id);
CREATE INDEX idx_accounts_type ON accounts(account_type_id);
CREATE INDEX idx_accounts_parent ON accounts(parent_id);

-- ============================================
-- FISCAL PERIODS
-- ============================================
CREATE TABLE fiscal_years (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    status VARCHAR(20) DEFAULT 'open', -- open, closed, locked
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, organization_id, start_date)
);

CREATE TABLE fiscal_periods (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    fiscal_year_id UUID NOT NULL REFERENCES fiscal_years(id) ON DELETE CASCADE,
    name VARCHAR(50) NOT NULL,
    period_number INTEGER NOT NULL,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    status VARCHAR(20) DEFAULT 'open', -- open, closed, locked
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_fiscal_periods_year ON fiscal_periods(fiscal_year_id);

-- ============================================
-- JOURNAL ENTRIES
-- ============================================
CREATE TABLE journals (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    code VARCHAR(20) NOT NULL,
    name VARCHAR(100) NOT NULL,
    type VARCHAR(50) NOT NULL, -- general, sales, purchase, cash, bank
    default_debit_account_id UUID REFERENCES accounts(id),
    default_credit_account_id UUID REFERENCES accounts(id),
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, code)
);

CREATE TABLE journal_entries (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    journal_id UUID NOT NULL REFERENCES journals(id),
    fiscal_period_id UUID REFERENCES fiscal_periods(id),
    entry_number VARCHAR(50) NOT NULL,
    entry_date DATE NOT NULL,
    reference VARCHAR(100),
    description TEXT,
    source_type VARCHAR(50), -- manual, invoice, payment, adjustment
    source_id UUID,
    currency_id UUID REFERENCES currencies(id),
    exchange_rate DECIMAL(20, 10) DEFAULT 1,
    total_debit DECIMAL(20, 4) DEFAULT 0,
    total_credit DECIMAL(20, 4) DEFAULT 0,
    status VARCHAR(20) DEFAULT 'draft', -- draft, posted, cancelled
    posted_at TIMESTAMP WITH TIME ZONE,
    posted_by UUID REFERENCES users(id),
    reversed_entry_id UUID REFERENCES journal_entries(id),
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, entry_number)
);

CREATE INDEX idx_journal_entries_tenant ON journal_entries(tenant_id);
CREATE INDEX idx_journal_entries_date ON journal_entries(entry_date);
CREATE INDEX idx_journal_entries_status ON journal_entries(status);

CREATE TABLE journal_entry_lines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    journal_entry_id UUID NOT NULL REFERENCES journal_entries(id) ON DELETE CASCADE,
    account_id UUID NOT NULL REFERENCES accounts(id),
    contact_id UUID REFERENCES contacts(id),
    description TEXT,
    debit_amount DECIMAL(20, 4) DEFAULT 0,
    credit_amount DECIMAL(20, 4) DEFAULT 0,
    currency_id UUID REFERENCES currencies(id),
    currency_amount DECIMAL(20, 4),
    exchange_rate DECIMAL(20, 10) DEFAULT 1,
    tax_id UUID,
    tax_amount DECIMAL(20, 4) DEFAULT 0,
    cost_center_id UUID,
    project_id UUID,
    reconciled BOOLEAN DEFAULT false,
    reconciled_at TIMESTAMP WITH TIME ZONE,
    line_number INTEGER NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_journal_entry_lines_entry ON journal_entry_lines(journal_entry_id);
CREATE INDEX idx_journal_entry_lines_account ON journal_entry_lines(account_id);

-- ============================================
-- TAXES
-- ============================================
CREATE TABLE tax_rates (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    code VARCHAR(20) NOT NULL,
    name VARCHAR(100) NOT NULL,
    description TEXT,
    rate DECIMAL(10, 4) NOT NULL,
    type VARCHAR(20) DEFAULT 'percentage', -- percentage, fixed
    tax_account_id UUID REFERENCES accounts(id),
    is_compound BOOLEAN DEFAULT false,
    is_recoverable BOOLEAN DEFAULT true,
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, code)
);

CREATE INDEX idx_tax_rates_tenant ON tax_rates(tenant_id);

-- ============================================
-- PRODUCTS & SERVICES
-- ============================================
CREATE TABLE product_categories (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    parent_id UUID REFERENCES product_categories(id) ON DELETE SET NULL,
    code VARCHAR(50) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    attributes JSONB DEFAULT '[]',
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, code)
);

CREATE INDEX idx_product_categories_tenant ON product_categories(tenant_id);
CREATE INDEX idx_product_categories_parent ON product_categories(parent_id);

CREATE TABLE units_of_measure (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    code VARCHAR(20) NOT NULL,
    name VARCHAR(100) NOT NULL,
    category VARCHAR(50), -- length, weight, volume, quantity
    base_unit_id UUID REFERENCES units_of_measure(id),
    conversion_factor DECIMAL(20, 10) DEFAULT 1,
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, code)
);

CREATE TABLE products (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    category_id UUID REFERENCES product_categories(id) ON DELETE SET NULL,
    type VARCHAR(20) NOT NULL, -- product, service, bundle
    code VARCHAR(50) NOT NULL,
    sku VARCHAR(100),
    barcode VARCHAR(100),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    short_description VARCHAR(500),
    unit_id UUID REFERENCES units_of_measure(id),
    purchase_unit_id UUID REFERENCES units_of_measure(id),
    sales_unit_id UUID REFERENCES units_of_measure(id),
    -- Pricing
    cost_price DECIMAL(20, 4) DEFAULT 0,
    list_price DECIMAL(20, 4) DEFAULT 0,
    min_price DECIMAL(20, 4) DEFAULT 0,
    currency_id UUID REFERENCES currencies(id),
    -- Inventory
    is_stockable BOOLEAN DEFAULT true,
    track_inventory BOOLEAN DEFAULT true,
    min_stock_level DECIMAL(20, 4) DEFAULT 0,
    max_stock_level DECIMAL(20, 4),
    reorder_point DECIMAL(20, 4) DEFAULT 0,
    reorder_quantity DECIMAL(20, 4) DEFAULT 0,
    lead_time_days INTEGER DEFAULT 0,
    -- Accounting
    sales_account_id UUID REFERENCES accounts(id),
    purchase_account_id UUID REFERENCES accounts(id),
    inventory_account_id UUID REFERENCES accounts(id),
    cogs_account_id UUID REFERENCES accounts(id),
    -- Tax
    sales_tax_id UUID REFERENCES tax_rates(id),
    purchase_tax_id UUID REFERENCES tax_rates(id),
    -- Dimensions
    weight DECIMAL(20, 4),
    weight_unit VARCHAR(20),
    length DECIMAL(20, 4),
    width DECIMAL(20, 4),
    height DECIMAL(20, 4),
    dimension_unit VARCHAR(20),
    -- Additional
    images JSONB DEFAULT '[]',
    attributes JSONB DEFAULT '{}',
    tags JSONB DEFAULT '[]',
    notes TEXT,
    is_purchasable BOOLEAN DEFAULT true,
    is_sellable BOOLEAN DEFAULT true,
    is_active BOOLEAN DEFAULT true,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, code)
);

CREATE INDEX idx_products_tenant ON products(tenant_id);
CREATE INDEX idx_products_category ON products(category_id);
CREATE INDEX idx_products_type ON products(tenant_id, type);
CREATE INDEX idx_products_sku ON products(tenant_id, sku);
CREATE INDEX idx_products_barcode ON products(tenant_id, barcode);
CREATE INDEX idx_products_name ON products USING gin(name gin_trgm_ops);

-- ============================================
-- WAREHOUSES & LOCATIONS
-- ============================================
CREATE TABLE warehouses (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    code VARCHAR(50) NOT NULL,
    name VARCHAR(255) NOT NULL,
    address JSONB,
    manager_id UUID REFERENCES users(id),
    is_default BOOLEAN DEFAULT false,
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, code)
);

CREATE INDEX idx_warehouses_tenant ON warehouses(tenant_id);

CREATE TABLE warehouse_locations (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    warehouse_id UUID NOT NULL REFERENCES warehouses(id) ON DELETE CASCADE,
    parent_id UUID REFERENCES warehouse_locations(id) ON DELETE SET NULL,
    code VARCHAR(50) NOT NULL,
    name VARCHAR(255) NOT NULL,
    type VARCHAR(50) DEFAULT 'storage', -- storage, receiving, shipping, staging
    aisle VARCHAR(20),
    rack VARCHAR(20),
    shelf VARCHAR(20),
    bin VARCHAR(20),
    capacity DECIMAL(20, 4),
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(warehouse_id, code)
);

CREATE INDEX idx_warehouse_locations_warehouse ON warehouse_locations(warehouse_id);

-- ============================================
-- INVENTORY
-- ============================================
CREATE TABLE inventory (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    warehouse_id UUID NOT NULL REFERENCES warehouses(id) ON DELETE CASCADE,
    location_id UUID REFERENCES warehouse_locations(id),
    lot_number VARCHAR(100),
    serial_number VARCHAR(100),
    expiry_date DATE,
    quantity_on_hand DECIMAL(20, 4) DEFAULT 0,
    quantity_reserved DECIMAL(20, 4) DEFAULT 0,
    quantity_available DECIMAL(20, 4) GENERATED ALWAYS AS (quantity_on_hand - quantity_reserved) STORED,
    unit_cost DECIMAL(20, 4) DEFAULT 0,
    total_value DECIMAL(20, 4) GENERATED ALWAYS AS (quantity_on_hand * unit_cost) STORED,
    last_count_date DATE,
    last_movement_date TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, product_id, warehouse_id, location_id, lot_number, serial_number)
);

CREATE INDEX idx_inventory_tenant ON inventory(tenant_id);
CREATE INDEX idx_inventory_product ON inventory(product_id);
CREATE INDEX idx_inventory_warehouse ON inventory(warehouse_id);

CREATE TABLE inventory_transactions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    inventory_id UUID NOT NULL REFERENCES inventory(id),
    transaction_type VARCHAR(50) NOT NULL, -- receipt, issue, transfer, adjustment, count
    reference_type VARCHAR(50),
    reference_id UUID,
    quantity DECIMAL(20, 4) NOT NULL,
    unit_cost DECIMAL(20, 4),
    total_cost DECIMAL(20, 4),
    from_warehouse_id UUID REFERENCES warehouses(id),
    to_warehouse_id UUID REFERENCES warehouses(id),
    from_location_id UUID REFERENCES warehouse_locations(id),
    to_location_id UUID REFERENCES warehouse_locations(id),
    reason VARCHAR(255),
    notes TEXT,
    transaction_date TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_inventory_transactions_tenant ON inventory_transactions(tenant_id);
CREATE INDEX idx_inventory_transactions_inventory ON inventory_transactions(inventory_id);
CREATE INDEX idx_inventory_transactions_date ON inventory_transactions(transaction_date);

-- ============================================
-- SALES ORDERS
-- ============================================
CREATE TABLE sales_orders (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id),
    order_number VARCHAR(50) NOT NULL,
    customer_id UUID NOT NULL REFERENCES contacts(id),
    contact_person_id UUID REFERENCES contact_persons(id),
    order_date DATE NOT NULL,
    expected_date DATE,
    -- Addresses
    billing_address JSONB,
    shipping_address JSONB,
    -- Amounts
    currency_id UUID REFERENCES currencies(id),
    exchange_rate DECIMAL(20, 10) DEFAULT 1,
    subtotal DECIMAL(20, 4) DEFAULT 0,
    discount_type VARCHAR(20), -- percentage, fixed
    discount_value DECIMAL(20, 4) DEFAULT 0,
    discount_amount DECIMAL(20, 4) DEFAULT 0,
    tax_amount DECIMAL(20, 4) DEFAULT 0,
    shipping_amount DECIMAL(20, 4) DEFAULT 0,
    total_amount DECIMAL(20, 4) DEFAULT 0,
    -- Status
    status VARCHAR(20) DEFAULT 'draft', -- draft, confirmed, processing, shipped, delivered, cancelled
    payment_status VARCHAR(20) DEFAULT 'unpaid', -- unpaid, partial, paid
    payment_terms INTEGER DEFAULT 30,
    -- References
    reference VARCHAR(100),
    po_number VARCHAR(100),
    notes TEXT,
    internal_notes TEXT,
    -- Tracking
    warehouse_id UUID REFERENCES warehouses(id),
    sales_rep_id UUID REFERENCES users(id),
    approved_by UUID REFERENCES users(id),
    approved_at TIMESTAMP WITH TIME ZONE,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, order_number)
);

CREATE INDEX idx_sales_orders_tenant ON sales_orders(tenant_id);
CREATE INDEX idx_sales_orders_customer ON sales_orders(customer_id);
CREATE INDEX idx_sales_orders_date ON sales_orders(order_date);
CREATE INDEX idx_sales_orders_status ON sales_orders(status);

CREATE TABLE sales_order_lines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    sales_order_id UUID NOT NULL REFERENCES sales_orders(id) ON DELETE CASCADE,
    line_number INTEGER NOT NULL,
    product_id UUID NOT NULL REFERENCES products(id),
    description TEXT,
    quantity DECIMAL(20, 4) NOT NULL,
    unit_id UUID REFERENCES units_of_measure(id),
    unit_price DECIMAL(20, 4) NOT NULL,
    discount_type VARCHAR(20),
    discount_value DECIMAL(20, 4) DEFAULT 0,
    discount_amount DECIMAL(20, 4) DEFAULT 0,
    tax_id UUID REFERENCES tax_rates(id),
    tax_amount DECIMAL(20, 4) DEFAULT 0,
    line_total DECIMAL(20, 4) DEFAULT 0,
    quantity_delivered DECIMAL(20, 4) DEFAULT 0,
    quantity_invoiced DECIMAL(20, 4) DEFAULT 0,
    warehouse_id UUID REFERENCES warehouses(id),
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_sales_order_lines_order ON sales_order_lines(sales_order_id);
CREATE INDEX idx_sales_order_lines_product ON sales_order_lines(product_id);

-- ============================================
-- SALES INVOICES
-- ============================================
CREATE TABLE sales_invoices (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id),
    invoice_number VARCHAR(50) NOT NULL,
    customer_id UUID NOT NULL REFERENCES contacts(id),
    sales_order_id UUID REFERENCES sales_orders(id),
    invoice_date DATE NOT NULL,
    due_date DATE NOT NULL,
    -- Addresses
    billing_address JSONB,
    shipping_address JSONB,
    -- Amounts
    currency_id UUID REFERENCES currencies(id),
    exchange_rate DECIMAL(20, 10) DEFAULT 1,
    subtotal DECIMAL(20, 4) DEFAULT 0,
    discount_amount DECIMAL(20, 4) DEFAULT 0,
    tax_amount DECIMAL(20, 4) DEFAULT 0,
    total_amount DECIMAL(20, 4) DEFAULT 0,
    amount_paid DECIMAL(20, 4) DEFAULT 0,
    amount_due DECIMAL(20, 4) GENERATED ALWAYS AS (total_amount - amount_paid) STORED,
    -- Status
    status VARCHAR(20) DEFAULT 'draft', -- draft, sent, partial, paid, overdue, cancelled
    -- References
    reference VARCHAR(100),
    po_number VARCHAR(100),
    notes TEXT,
    terms_conditions TEXT,
    -- Journal
    journal_entry_id UUID REFERENCES journal_entries(id),
    -- Tracking
    sent_at TIMESTAMP WITH TIME ZONE,
    viewed_at TIMESTAMP WITH TIME ZONE,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, invoice_number)
);

CREATE INDEX idx_sales_invoices_tenant ON sales_invoices(tenant_id);
CREATE INDEX idx_sales_invoices_customer ON sales_invoices(customer_id);
CREATE INDEX idx_sales_invoices_date ON sales_invoices(invoice_date);
CREATE INDEX idx_sales_invoices_status ON sales_invoices(status);
CREATE INDEX idx_sales_invoices_due_date ON sales_invoices(due_date);

CREATE TABLE sales_invoice_lines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    sales_invoice_id UUID NOT NULL REFERENCES sales_invoices(id) ON DELETE CASCADE,
    sales_order_line_id UUID REFERENCES sales_order_lines(id),
    line_number INTEGER NOT NULL,
    product_id UUID REFERENCES products(id),
    description TEXT NOT NULL,
    quantity DECIMAL(20, 4) NOT NULL,
    unit_id UUID REFERENCES units_of_measure(id),
    unit_price DECIMAL(20, 4) NOT NULL,
    discount_amount DECIMAL(20, 4) DEFAULT 0,
    tax_id UUID REFERENCES tax_rates(id),
    tax_amount DECIMAL(20, 4) DEFAULT 0,
    line_total DECIMAL(20, 4) DEFAULT 0,
    account_id UUID REFERENCES accounts(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_sales_invoice_lines_invoice ON sales_invoice_lines(sales_invoice_id);

-- ============================================
-- PURCHASE ORDERS
-- ============================================
CREATE TABLE purchase_orders (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id),
    order_number VARCHAR(50) NOT NULL,
    vendor_id UUID NOT NULL REFERENCES contacts(id),
    contact_person_id UUID REFERENCES contact_persons(id),
    order_date DATE NOT NULL,
    expected_date DATE,
    -- Amounts
    currency_id UUID REFERENCES currencies(id),
    exchange_rate DECIMAL(20, 10) DEFAULT 1,
    subtotal DECIMAL(20, 4) DEFAULT 0,
    discount_amount DECIMAL(20, 4) DEFAULT 0,
    tax_amount DECIMAL(20, 4) DEFAULT 0,
    shipping_amount DECIMAL(20, 4) DEFAULT 0,
    total_amount DECIMAL(20, 4) DEFAULT 0,
    -- Status
    status VARCHAR(20) DEFAULT 'draft', -- draft, pending_approval, approved, ordered, partial, received, cancelled
    payment_status VARCHAR(20) DEFAULT 'unpaid',
    payment_terms INTEGER DEFAULT 30,
    -- References
    reference VARCHAR(100),
    vendor_reference VARCHAR(100),
    notes TEXT,
    internal_notes TEXT,
    -- Tracking
    warehouse_id UUID REFERENCES warehouses(id),
    requested_by UUID REFERENCES users(id),
    approved_by UUID REFERENCES users(id),
    approved_at TIMESTAMP WITH TIME ZONE,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, order_number)
);

CREATE INDEX idx_purchase_orders_tenant ON purchase_orders(tenant_id);
CREATE INDEX idx_purchase_orders_vendor ON purchase_orders(vendor_id);
CREATE INDEX idx_purchase_orders_date ON purchase_orders(order_date);
CREATE INDEX idx_purchase_orders_status ON purchase_orders(status);

CREATE TABLE purchase_order_lines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    purchase_order_id UUID NOT NULL REFERENCES purchase_orders(id) ON DELETE CASCADE,
    line_number INTEGER NOT NULL,
    product_id UUID NOT NULL REFERENCES products(id),
    description TEXT,
    quantity DECIMAL(20, 4) NOT NULL,
    unit_id UUID REFERENCES units_of_measure(id),
    unit_price DECIMAL(20, 4) NOT NULL,
    discount_amount DECIMAL(20, 4) DEFAULT 0,
    tax_id UUID REFERENCES tax_rates(id),
    tax_amount DECIMAL(20, 4) DEFAULT 0,
    line_total DECIMAL(20, 4) DEFAULT 0,
    quantity_received DECIMAL(20, 4) DEFAULT 0,
    quantity_invoiced DECIMAL(20, 4) DEFAULT 0,
    warehouse_id UUID REFERENCES warehouses(id),
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_purchase_order_lines_order ON purchase_order_lines(purchase_order_id);
CREATE INDEX idx_purchase_order_lines_product ON purchase_order_lines(product_id);

-- ============================================
-- PAYMENTS
-- ============================================
CREATE TABLE payment_methods (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    code VARCHAR(20) NOT NULL,
    name VARCHAR(100) NOT NULL,
    type VARCHAR(50) NOT NULL, -- cash, bank_transfer, credit_card, check, other
    account_id UUID REFERENCES accounts(id),
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, code)
);

CREATE TABLE payments (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id),
    payment_number VARCHAR(50) NOT NULL,
    type VARCHAR(20) NOT NULL, -- receipt, payment
    contact_id UUID NOT NULL REFERENCES contacts(id),
    payment_method_id UUID REFERENCES payment_methods(id),
    payment_date DATE NOT NULL,
    amount DECIMAL(20, 4) NOT NULL,
    currency_id UUID REFERENCES currencies(id),
    exchange_rate DECIMAL(20, 10) DEFAULT 1,
    reference VARCHAR(100),
    notes TEXT,
    status VARCHAR(20) DEFAULT 'draft', -- draft, confirmed, cancelled
    journal_entry_id UUID REFERENCES journal_entries(id),
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, payment_number)
);

CREATE INDEX idx_payments_tenant ON payments(tenant_id);
CREATE INDEX idx_payments_contact ON payments(contact_id);
CREATE INDEX idx_payments_date ON payments(payment_date);

CREATE TABLE payment_allocations (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    payment_id UUID NOT NULL REFERENCES payments(id) ON DELETE CASCADE,
    document_type VARCHAR(50) NOT NULL, -- sales_invoice, purchase_invoice
    document_id UUID NOT NULL,
    amount DECIMAL(20, 4) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_payment_allocations_payment ON payment_allocations(payment_id);
CREATE INDEX idx_payment_allocations_document ON payment_allocations(document_type, document_id);

-- ============================================
-- HR MODULE - EMPLOYEES
-- ============================================
CREATE TABLE employees (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    organization_id UUID REFERENCES organizations(id),
    department_id UUID REFERENCES departments(id),
    employee_number VARCHAR(50) NOT NULL,
    first_name VARCHAR(100) NOT NULL,
    last_name VARCHAR(100) NOT NULL,
    middle_name VARCHAR(100),
    email VARCHAR(255),
    phone VARCHAR(50),
    mobile VARCHAR(50),
    date_of_birth DATE,
    gender VARCHAR(20),
    nationality VARCHAR(100),
    marital_status VARCHAR(20),
    -- Employment
    employment_type VARCHAR(50), -- full_time, part_time, contract, intern
    job_title VARCHAR(100),
    job_grade VARCHAR(50),
    hire_date DATE NOT NULL,
    probation_end_date DATE,
    termination_date DATE,
    termination_reason TEXT,
    manager_id UUID REFERENCES employees(id),
    -- Compensation
    base_salary DECIMAL(20, 4),
    salary_currency_id UUID REFERENCES currencies(id),
    pay_frequency VARCHAR(20), -- monthly, bi_weekly, weekly
    bank_account JSONB,
    -- Documents
    national_id VARCHAR(50),
    passport_number VARCHAR(50),
    passport_expiry DATE,
    work_permit_number VARCHAR(50),
    work_permit_expiry DATE,
    -- Contact
    address JSONB,
    emergency_contact JSONB,
    -- Status
    status VARCHAR(20) DEFAULT 'active', -- active, on_leave, suspended, terminated
    notes TEXT,
    custom_fields JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, employee_number)
);

CREATE INDEX idx_employees_tenant ON employees(tenant_id);
CREATE INDEX idx_employees_department ON employees(department_id);
CREATE INDEX idx_employees_manager ON employees(manager_id);
CREATE INDEX idx_employees_status ON employees(status);

-- ============================================
-- ADD TRIGGERS FOR NEW TABLES
-- ============================================
CREATE TRIGGER update_contacts_updated_at BEFORE UPDATE ON contacts FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_contact_persons_updated_at BEFORE UPDATE ON contact_persons FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_accounts_updated_at BEFORE UPDATE ON accounts FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_fiscal_years_updated_at BEFORE UPDATE ON fiscal_years FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_fiscal_periods_updated_at BEFORE UPDATE ON fiscal_periods FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_journal_entries_updated_at BEFORE UPDATE ON journal_entries FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_tax_rates_updated_at BEFORE UPDATE ON tax_rates FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_product_categories_updated_at BEFORE UPDATE ON product_categories FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_products_updated_at BEFORE UPDATE ON products FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_warehouses_updated_at BEFORE UPDATE ON warehouses FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_warehouse_locations_updated_at BEFORE UPDATE ON warehouse_locations FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_inventory_updated_at BEFORE UPDATE ON inventory FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_sales_orders_updated_at BEFORE UPDATE ON sales_orders FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_sales_order_lines_updated_at BEFORE UPDATE ON sales_order_lines FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_sales_invoices_updated_at BEFORE UPDATE ON sales_invoices FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_purchase_orders_updated_at BEFORE UPDATE ON purchase_orders FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_purchase_order_lines_updated_at BEFORE UPDATE ON purchase_order_lines FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_payments_updated_at BEFORE UPDATE ON payments FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_employees_updated_at BEFORE UPDATE ON employees FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- ============================================
-- ADD MORE PERMISSIONS FOR ERP MODULES
-- ============================================
INSERT INTO permissions (module, resource, action, description) VALUES
-- Contacts
('contacts', 'customer', 'create', 'Create customers'),
('contacts', 'customer', 'read', 'View customers'),
('contacts', 'customer', 'update', 'Update customers'),
('contacts', 'customer', 'delete', 'Delete customers'),
('contacts', 'vendor', 'create', 'Create vendors'),
('contacts', 'vendor', 'read', 'View vendors'),
('contacts', 'vendor', 'update', 'Update vendors'),
('contacts', 'vendor', 'delete', 'Delete vendors'),
-- Products
('inventory', 'product', 'create', 'Create products'),
('inventory', 'product', 'read', 'View products'),
('inventory', 'product', 'update', 'Update products'),
('inventory', 'product', 'delete', 'Delete products'),
('inventory', 'category', 'manage', 'Manage product categories'),
-- Inventory
('inventory', 'stock', 'read', 'View inventory'),
('inventory', 'stock', 'adjust', 'Adjust inventory'),
('inventory', 'stock', 'transfer', 'Transfer inventory'),
('inventory', 'warehouse', 'manage', 'Manage warehouses'),
-- Sales
('sales', 'order', 'create', 'Create sales orders'),
('sales', 'order', 'read', 'View sales orders'),
('sales', 'order', 'update', 'Update sales orders'),
('sales', 'order', 'delete', 'Delete sales orders'),
('sales', 'order', 'approve', 'Approve sales orders'),
('sales', 'invoice', 'create', 'Create sales invoices'),
('sales', 'invoice', 'read', 'View sales invoices'),
('sales', 'invoice', 'update', 'Update sales invoices'),
('sales', 'invoice', 'delete', 'Delete sales invoices'),
-- Purchase
('purchase', 'order', 'create', 'Create purchase orders'),
('purchase', 'order', 'read', 'View purchase orders'),
('purchase', 'order', 'update', 'Update purchase orders'),
('purchase', 'order', 'delete', 'Delete purchase orders'),
('purchase', 'order', 'approve', 'Approve purchase orders'),
-- Finance
('finance', 'account', 'create', 'Create accounts'),
('finance', 'account', 'read', 'View chart of accounts'),
('finance', 'account', 'update', 'Update accounts'),
('finance', 'account', 'delete', 'Delete accounts'),
('finance', 'journal', 'create', 'Create journal entries'),
('finance', 'journal', 'read', 'View journal entries'),
('finance', 'journal', 'post', 'Post journal entries'),
('finance', 'journal', 'reverse', 'Reverse journal entries'),
('finance', 'payment', 'create', 'Create payments'),
('finance', 'payment', 'read', 'View payments'),
('finance', 'payment', 'approve', 'Approve payments'),
('finance', 'report', 'read', 'View financial reports'),
-- HR
('hr', 'employee', 'create', 'Create employees'),
('hr', 'employee', 'read', 'View employees'),
('hr', 'employee', 'update', 'Update employees'),
('hr', 'employee', 'delete', 'Delete employees'),
('hr', 'payroll', 'process', 'Process payroll'),
('hr', 'payroll', 'read', 'View payroll'),
('hr', 'leave', 'manage', 'Manage leave requests');
