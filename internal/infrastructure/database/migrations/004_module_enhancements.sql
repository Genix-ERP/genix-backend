-- Migration: 003_module_enhancements.sql
-- Description: Add enhancements for Finance and Inventory modules
-- Date: 2026-01-13

-- =====================================================
-- FINANCE MODULE ENHANCEMENTS
-- =====================================================

-- 1. Bank Accounts (extends accounts for bank reconciliation)
CREATE TABLE IF NOT EXISTS bank_accounts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    bank_name VARCHAR(255) NOT NULL,
    account_number VARCHAR(100),
    routing_number VARCHAR(50),
    swift_code VARCHAR(20),
    iban VARCHAR(50),
    branch_name VARCHAR(255),
    branch_address TEXT,
    contact_person VARCHAR(255),
    contact_phone VARCHAR(50),
    last_reconciled_date DATE,
    last_reconciled_balance DECIMAL(20, 4) DEFAULT 0,
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, account_id)
);

-- 2. Bank Transactions (for bank statement import and reconciliation)
CREATE TABLE IF NOT EXISTS bank_transactions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    bank_account_id UUID NOT NULL REFERENCES bank_accounts(id) ON DELETE CASCADE,
    transaction_date DATE NOT NULL,
    value_date DATE,
    reference VARCHAR(100),
    description TEXT,
    amount DECIMAL(20, 4) NOT NULL,
    balance_after DECIMAL(20, 4),
    transaction_type VARCHAR(20) NOT NULL, -- debit, credit
    status VARCHAR(20) DEFAULT 'unmatched', -- unmatched, matched, reconciled
    matched_journal_entry_id UUID REFERENCES journal_entries(id),
    import_batch_id VARCHAR(100),
    raw_data JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 3. Bank Reconciliations
CREATE TABLE IF NOT EXISTS bank_reconciliations (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    bank_account_id UUID NOT NULL REFERENCES bank_accounts(id) ON DELETE CASCADE,
    statement_date DATE NOT NULL,
    statement_ending_balance DECIMAL(20, 4) NOT NULL,
    book_balance DECIMAL(20, 4) NOT NULL,
    reconciled_balance DECIMAL(20, 4),
    difference DECIMAL(20, 4),
    status VARCHAR(20) DEFAULT 'draft', -- draft, completed
    completed_at TIMESTAMP WITH TIME ZONE,
    completed_by UUID REFERENCES users(id),
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 4. Cash Transactions (for petty cash management)
CREATE TABLE IF NOT EXISTS cash_transactions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id),
    transaction_number VARCHAR(50) NOT NULL,
    transaction_type VARCHAR(20) NOT NULL, -- receipt, disbursement
    transaction_date DATE NOT NULL,
    account_id UUID NOT NULL REFERENCES accounts(id),
    contact_id UUID REFERENCES contacts(id),
    amount DECIMAL(20, 4) NOT NULL,
    currency_id UUID REFERENCES currencies(id),
    exchange_rate DECIMAL(20, 10) DEFAULT 1,
    description TEXT,
    reference VARCHAR(100),
    status VARCHAR(20) DEFAULT 'draft', -- draft, posted, cancelled
    journal_entry_id UUID REFERENCES journal_entries(id),
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, transaction_number)
);

-- 5. Purchase Invoices (Vendor Bills - AP)
CREATE TABLE IF NOT EXISTS purchase_invoices (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id),
    invoice_number VARCHAR(50) NOT NULL,
    vendor_id UUID NOT NULL REFERENCES contacts(id),
    purchase_order_id UUID REFERENCES purchase_orders(id),
    vendor_invoice_number VARCHAR(100),
    invoice_date DATE NOT NULL,
    due_date DATE NOT NULL,
    currency_id UUID REFERENCES currencies(id),
    exchange_rate DECIMAL(20, 10) DEFAULT 1,
    subtotal DECIMAL(20, 4) DEFAULT 0,
    discount_percent DECIMAL(5, 2) DEFAULT 0,
    discount_amount DECIMAL(20, 4) DEFAULT 0,
    tax_amount DECIMAL(20, 4) DEFAULT 0,
    total_amount DECIMAL(20, 4) DEFAULT 0,
    amount_paid DECIMAL(20, 4) DEFAULT 0,
    amount_due DECIMAL(20, 4) GENERATED ALWAYS AS (total_amount - amount_paid) STORED,
    status VARCHAR(20) DEFAULT 'draft', -- draft, confirmed, partial, paid, cancelled
    payment_status VARCHAR(20) DEFAULT 'unpaid', -- unpaid, partial, paid
    three_way_match_status VARCHAR(20) DEFAULT 'pending', -- pending, matched, exception
    journal_entry_id UUID REFERENCES journal_entries(id),
    notes TEXT,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, invoice_number)
);

CREATE TABLE IF NOT EXISTS purchase_invoice_lines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    purchase_invoice_id UUID NOT NULL REFERENCES purchase_invoices(id) ON DELETE CASCADE,
    purchase_order_line_id UUID REFERENCES purchase_order_lines(id),
    line_number INTEGER NOT NULL,
    product_id UUID REFERENCES products(id),
    description TEXT NOT NULL,
    quantity DECIMAL(20, 4) NOT NULL,
    unit_id UUID REFERENCES units_of_measure(id),
    unit_price DECIMAL(20, 4) NOT NULL,
    discount_percent DECIMAL(5, 2) DEFAULT 0,
    discount_amount DECIMAL(20, 4) DEFAULT 0,
    tax_id UUID REFERENCES tax_rates(id),
    tax_amount DECIMAL(20, 4) DEFAULT 0,
    line_total DECIMAL(20, 4) DEFAULT 0,
    account_id UUID REFERENCES accounts(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 6. Budgets
CREATE TABLE IF NOT EXISTS budgets (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id),
    fiscal_year_id UUID NOT NULL REFERENCES fiscal_years(id),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    budget_type VARCHAR(20) DEFAULT 'expense', -- expense, revenue, capital
    total_amount DECIMAL(20, 4) DEFAULT 0,
    status VARCHAR(20) DEFAULT 'draft', -- draft, approved, active, closed
    approved_by UUID REFERENCES users(id),
    approved_at TIMESTAMP WITH TIME ZONE,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE IF NOT EXISTS budget_lines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    budget_id UUID NOT NULL REFERENCES budgets(id) ON DELETE CASCADE,
    account_id UUID NOT NULL REFERENCES accounts(id),
    fiscal_period_id UUID REFERENCES fiscal_periods(id),
    department_id UUID REFERENCES departments(id),
    budgeted_amount DECIMAL(20, 4) NOT NULL,
    actual_amount DECIMAL(20, 4) DEFAULT 0,
    variance DECIMAL(20, 4) GENERATED ALWAYS AS (budgeted_amount - actual_amount) STORED,
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- =====================================================
-- INVENTORY MODULE ENHANCEMENTS
-- =====================================================

-- 7. Inventory Lots/Batches for FIFO/LIFO tracking
CREATE TABLE IF NOT EXISTS inventory_lots (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES products(id),
    warehouse_id UUID NOT NULL REFERENCES warehouses(id),
    location_id UUID REFERENCES warehouse_locations(id),
    lot_number VARCHAR(100) NOT NULL,
    serial_number VARCHAR(100),
    received_date DATE NOT NULL,
    expiry_date DATE,
    manufacture_date DATE,
    initial_quantity DECIMAL(20, 4) NOT NULL,
    remaining_quantity DECIMAL(20, 4) NOT NULL,
    unit_cost DECIMAL(20, 4) NOT NULL,
    vendor_id UUID REFERENCES contacts(id),
    purchase_order_id UUID REFERENCES purchase_orders(id),
    purchase_invoice_id UUID REFERENCES purchase_invoices(id),
    status VARCHAR(20) DEFAULT 'available', -- available, reserved, depleted, expired, quarantine
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, product_id, warehouse_id, lot_number)
);

-- 8. Transfer Orders (Inter-warehouse transfers)
CREATE TABLE IF NOT EXISTS transfer_orders (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    transfer_number VARCHAR(50) NOT NULL,
    from_warehouse_id UUID NOT NULL REFERENCES warehouses(id),
    to_warehouse_id UUID NOT NULL REFERENCES warehouses(id),
    transfer_date DATE NOT NULL,
    expected_arrival DATE,
    actual_arrival DATE,
    status VARCHAR(20) DEFAULT 'draft', -- draft, pending, approved, in_transit, received, cancelled
    priority VARCHAR(20) DEFAULT 'normal', -- low, normal, high, urgent
    notes TEXT,
    reason TEXT,
    created_by UUID REFERENCES users(id),
    approved_by UUID REFERENCES users(id),
    approved_at TIMESTAMP WITH TIME ZONE,
    shipped_by UUID REFERENCES users(id),
    shipped_at TIMESTAMP WITH TIME ZONE,
    received_by UUID REFERENCES users(id),
    received_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, transfer_number)
);

CREATE TABLE IF NOT EXISTS transfer_order_lines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    transfer_order_id UUID NOT NULL REFERENCES transfer_orders(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES products(id),
    from_location_id UUID REFERENCES warehouse_locations(id),
    to_location_id UUID REFERENCES warehouse_locations(id),
    lot_id UUID REFERENCES inventory_lots(id),
    lot_number VARCHAR(100),
    serial_number VARCHAR(100),
    quantity_requested DECIMAL(20, 4) NOT NULL,
    quantity_shipped DECIMAL(20, 4) DEFAULT 0,
    quantity_received DECIMAL(20, 4) DEFAULT 0,
    quantity_variance DECIMAL(20, 4) GENERATED ALWAYS AS (quantity_shipped - quantity_received) STORED,
    unit_cost DECIMAL(20, 4),
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 9. Stock Counts (Inventory counting sessions)
CREATE TABLE IF NOT EXISTS stock_counts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    warehouse_id UUID NOT NULL REFERENCES warehouses(id),
    count_number VARCHAR(50) NOT NULL,
    count_type VARCHAR(20) DEFAULT 'full', -- full, cycle, spot
    count_date DATE NOT NULL,
    status VARCHAR(20) DEFAULT 'draft', -- draft, in_progress, completed, cancelled, approved
    notes TEXT,
    started_at TIMESTAMP WITH TIME ZONE,
    started_by UUID REFERENCES users(id),
    completed_at TIMESTAMP WITH TIME ZONE,
    completed_by UUID REFERENCES users(id),
    approved_at TIMESTAMP WITH TIME ZONE,
    approved_by UUID REFERENCES users(id),
    total_system_value DECIMAL(20, 4) DEFAULT 0,
    total_counted_value DECIMAL(20, 4) DEFAULT 0,
    total_variance_value DECIMAL(20, 4) DEFAULT 0,
    adjustment_journal_id UUID REFERENCES journal_entries(id),
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, count_number)
);

CREATE TABLE IF NOT EXISTS stock_count_lines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    stock_count_id UUID NOT NULL REFERENCES stock_counts(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES products(id),
    location_id UUID REFERENCES warehouse_locations(id),
    lot_id UUID REFERENCES inventory_lots(id),
    lot_number VARCHAR(100),
    serial_number VARCHAR(100),
    system_quantity DECIMAL(20, 4) NOT NULL,
    counted_quantity DECIMAL(20, 4),
    variance_quantity DECIMAL(20, 4) GENERATED ALWAYS AS (COALESCE(counted_quantity, 0) - system_quantity) STORED,
    unit_cost DECIMAL(20, 4),
    system_value DECIMAL(20, 4),
    counted_value DECIMAL(20, 4),
    variance_value DECIMAL(20, 4),
    status VARCHAR(20) DEFAULT 'pending', -- pending, counted, verified, adjusted
    counted_by UUID REFERENCES users(id),
    counted_at TIMESTAMP WITH TIME ZONE,
    verified_by UUID REFERENCES users(id),
    verified_at TIMESTAMP WITH TIME ZONE,
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 10. Reorder Alerts
CREATE TABLE IF NOT EXISTS reorder_alerts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES products(id),
    warehouse_id UUID REFERENCES warehouses(id),
    alert_type VARCHAR(20) NOT NULL, -- low_stock, out_of_stock, expiring, overstock
    current_quantity DECIMAL(20, 4),
    threshold_quantity DECIMAL(20, 4),
    suggested_order_qty DECIMAL(20, 4),
    expiry_date DATE,
    days_until_expiry INTEGER,
    priority VARCHAR(20) DEFAULT 'medium', -- low, medium, high, critical
    status VARCHAR(20) DEFAULT 'active', -- active, acknowledged, resolved, ignored, snoozed
    snoozed_until TIMESTAMP WITH TIME ZONE,
    acknowledged_by UUID REFERENCES users(id),
    acknowledged_at TIMESTAMP WITH TIME ZONE,
    resolved_at TIMESTAMP WITH TIME ZONE,
    resolved_by UUID REFERENCES users(id),
    resolution_notes TEXT,
    related_purchase_order_id UUID REFERENCES purchase_orders(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- =====================================================
-- COLUMN ADDITIONS TO EXISTING TABLES
-- =====================================================

-- Add costing_method to products
ALTER TABLE products ADD COLUMN IF NOT EXISTS costing_method VARCHAR(20) DEFAULT 'weighted_average';
-- Options: fifo, lifo, weighted_average, standard

-- Add budget tracking flag to accounts
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS budget_tracking BOOLEAN DEFAULT false;

-- Add auto sequence settings to journals
ALTER TABLE journals ADD COLUMN IF NOT EXISTS auto_sequence BOOLEAN DEFAULT true;
ALTER TABLE journals ADD COLUMN IF NOT EXISTS next_number INTEGER DEFAULT 1;
ALTER TABLE journals ADD COLUMN IF NOT EXISTS number_prefix VARCHAR(20);

-- Add approval workflow fields to payments
ALTER TABLE payments ADD COLUMN IF NOT EXISTS approved_by UUID REFERENCES users(id);
ALTER TABLE payments ADD COLUMN IF NOT EXISTS approved_at TIMESTAMP WITH TIME ZONE;

-- =====================================================
-- INDEXES
-- =====================================================

-- Finance indexes
CREATE INDEX IF NOT EXISTS idx_bank_accounts_tenant ON bank_accounts(tenant_id);
CREATE INDEX IF NOT EXISTS idx_bank_accounts_account ON bank_accounts(account_id);
CREATE INDEX IF NOT EXISTS idx_bank_transactions_account ON bank_transactions(bank_account_id);
CREATE INDEX IF NOT EXISTS idx_bank_transactions_date ON bank_transactions(transaction_date);
CREATE INDEX IF NOT EXISTS idx_bank_transactions_status ON bank_transactions(status);
CREATE INDEX IF NOT EXISTS idx_cash_transactions_tenant ON cash_transactions(tenant_id);
CREATE INDEX IF NOT EXISTS idx_cash_transactions_date ON cash_transactions(transaction_date);
CREATE INDEX IF NOT EXISTS idx_purchase_invoices_tenant ON purchase_invoices(tenant_id);
CREATE INDEX IF NOT EXISTS idx_purchase_invoices_vendor ON purchase_invoices(vendor_id);
CREATE INDEX IF NOT EXISTS idx_purchase_invoices_status ON purchase_invoices(status);
CREATE INDEX IF NOT EXISTS idx_purchase_invoices_due_date ON purchase_invoices(due_date);
CREATE INDEX IF NOT EXISTS idx_budgets_tenant ON budgets(tenant_id);
CREATE INDEX IF NOT EXISTS idx_budgets_fiscal_year ON budgets(fiscal_year_id);
CREATE INDEX IF NOT EXISTS idx_budget_lines_budget ON budget_lines(budget_id);
CREATE INDEX IF NOT EXISTS idx_budget_lines_account ON budget_lines(account_id);

-- Inventory indexes
CREATE INDEX IF NOT EXISTS idx_inventory_lots_tenant ON inventory_lots(tenant_id);
CREATE INDEX IF NOT EXISTS idx_inventory_lots_product ON inventory_lots(product_id);
CREATE INDEX IF NOT EXISTS idx_inventory_lots_warehouse ON inventory_lots(warehouse_id);
CREATE INDEX IF NOT EXISTS idx_inventory_lots_status ON inventory_lots(status);
CREATE INDEX IF NOT EXISTS idx_inventory_lots_expiry ON inventory_lots(expiry_date);
CREATE INDEX IF NOT EXISTS idx_inventory_lots_lot_number ON inventory_lots(lot_number);
CREATE INDEX IF NOT EXISTS idx_transfer_orders_tenant ON transfer_orders(tenant_id);
CREATE INDEX IF NOT EXISTS idx_transfer_orders_status ON transfer_orders(status);
CREATE INDEX IF NOT EXISTS idx_transfer_orders_from_warehouse ON transfer_orders(from_warehouse_id);
CREATE INDEX IF NOT EXISTS idx_transfer_orders_to_warehouse ON transfer_orders(to_warehouse_id);
CREATE INDEX IF NOT EXISTS idx_stock_counts_tenant ON stock_counts(tenant_id);
CREATE INDEX IF NOT EXISTS idx_stock_counts_warehouse ON stock_counts(warehouse_id);
CREATE INDEX IF NOT EXISTS idx_stock_counts_status ON stock_counts(status);
CREATE INDEX IF NOT EXISTS idx_stock_count_lines_product ON stock_count_lines(product_id);
CREATE INDEX IF NOT EXISTS idx_reorder_alerts_tenant ON reorder_alerts(tenant_id);
CREATE INDEX IF NOT EXISTS idx_reorder_alerts_product ON reorder_alerts(product_id);
CREATE INDEX IF NOT EXISTS idx_reorder_alerts_status ON reorder_alerts(status);
CREATE INDEX IF NOT EXISTS idx_reorder_alerts_priority ON reorder_alerts(priority);

-- =====================================================
-- TRIGGERS
-- =====================================================

-- Update timestamps triggers
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_bank_accounts_updated_at') THEN
        CREATE TRIGGER update_bank_accounts_updated_at BEFORE UPDATE ON bank_accounts
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_bank_transactions_updated_at') THEN
        CREATE TRIGGER update_bank_transactions_updated_at BEFORE UPDATE ON bank_transactions
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_bank_reconciliations_updated_at') THEN
        CREATE TRIGGER update_bank_reconciliations_updated_at BEFORE UPDATE ON bank_reconciliations
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_cash_transactions_updated_at') THEN
        CREATE TRIGGER update_cash_transactions_updated_at BEFORE UPDATE ON cash_transactions
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_purchase_invoices_updated_at') THEN
        CREATE TRIGGER update_purchase_invoices_updated_at BEFORE UPDATE ON purchase_invoices
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_budgets_updated_at') THEN
        CREATE TRIGGER update_budgets_updated_at BEFORE UPDATE ON budgets
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_budget_lines_updated_at') THEN
        CREATE TRIGGER update_budget_lines_updated_at BEFORE UPDATE ON budget_lines
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_inventory_lots_updated_at') THEN
        CREATE TRIGGER update_inventory_lots_updated_at BEFORE UPDATE ON inventory_lots
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_transfer_orders_updated_at') THEN
        CREATE TRIGGER update_transfer_orders_updated_at BEFORE UPDATE ON transfer_orders
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_transfer_order_lines_updated_at') THEN
        CREATE TRIGGER update_transfer_order_lines_updated_at BEFORE UPDATE ON transfer_order_lines
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_stock_counts_updated_at') THEN
        CREATE TRIGGER update_stock_counts_updated_at BEFORE UPDATE ON stock_counts
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_stock_count_lines_updated_at') THEN
        CREATE TRIGGER update_stock_count_lines_updated_at BEFORE UPDATE ON stock_count_lines
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_reorder_alerts_updated_at') THEN
        CREATE TRIGGER update_reorder_alerts_updated_at BEFORE UPDATE ON reorder_alerts
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;
END $$;
