-- GenixERP Finance Extra Modules Migration
-- Kassa, Valyuta (exchange diff), Akt sverka, Byudjet enhancements

-- ============================================
-- CASH REGISTERS (Kassalar)
-- ============================================
CREATE TABLE IF NOT EXISTS cash_registers (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    name VARCHAR(255) NOT NULL,
    code VARCHAR(50),
    currency VARCHAR(10) NOT NULL DEFAULT 'UZS',
    limit_amount DECIMAL(20, 4) DEFAULT 0,
    current_balance DECIMAL(20, 4) DEFAULT 0,
    is_active BOOLEAN DEFAULT true,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, code)
);

CREATE INDEX idx_cash_registers_tenant ON cash_registers(tenant_id);

-- ============================================
-- CASH ORDERS (PKO / RKO)
-- ============================================
CREATE TABLE IF NOT EXISTS cash_orders (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    cash_register_id UUID NOT NULL REFERENCES cash_registers(id) ON DELETE RESTRICT,
    order_number VARCHAR(50) NOT NULL,
    order_type VARCHAR(10) NOT NULL CHECK (order_type IN ('pko', 'rko')),
    order_date DATE NOT NULL DEFAULT CURRENT_DATE,
    amount DECIMAL(20, 4) NOT NULL CHECK (amount > 0),
    currency VARCHAR(10) NOT NULL DEFAULT 'UZS',
    partner_id UUID REFERENCES contacts(id) ON DELETE SET NULL,
    account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
    description TEXT NOT NULL,
    cashier_id UUID REFERENCES users(id),
    journal_entry_id UUID REFERENCES journal_entries(id) ON DELETE SET NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'confirmed', 'cancelled')),
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, order_number)
);

CREATE INDEX idx_cash_orders_tenant ON cash_orders(tenant_id);
CREATE INDEX idx_cash_orders_register ON cash_orders(cash_register_id);
CREATE INDEX idx_cash_orders_date ON cash_orders(tenant_id, order_date);
CREATE INDEX idx_cash_orders_type ON cash_orders(tenant_id, order_type);
CREATE INDEX idx_cash_orders_partner ON cash_orders(partner_id);

-- ============================================
-- CASH BOOK ENTRIES (Kassa kitob)
-- ============================================
CREATE TABLE IF NOT EXISTS cash_book_entries (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    cash_register_id UUID NOT NULL REFERENCES cash_registers(id) ON DELETE CASCADE,
    entry_date DATE NOT NULL,
    opening_balance DECIMAL(20, 4) NOT NULL DEFAULT 0,
    total_income DECIMAL(20, 4) NOT NULL DEFAULT 0,
    total_expense DECIMAL(20, 4) NOT NULL DEFAULT 0,
    closing_balance DECIMAL(20, 4) GENERATED ALWAYS AS (opening_balance + total_income - total_expense) STORED,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, cash_register_id, entry_date)
);

CREATE INDEX idx_cash_book_entries_tenant ON cash_book_entries(tenant_id);
CREATE INDEX idx_cash_book_entries_date ON cash_book_entries(cash_register_id, entry_date);

-- ============================================
-- EXCHANGE RATE DIFFERENCES (Kurs farqlari)
-- ============================================
CREATE TABLE IF NOT EXISTS exchange_diffs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    currency_id UUID NOT NULL REFERENCES currencies(id) ON DELETE RESTRICT,
    amount_uzs DECIMAL(20, 4) NOT NULL,
    diff_type VARCHAR(10) NOT NULL CHECK (diff_type IN ('positive', 'negative')),
    period_start DATE NOT NULL,
    period_end DATE NOT NULL,
    journal_entry_id UUID REFERENCES journal_entries(id) ON DELETE SET NULL,
    description TEXT,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX idx_exchange_diffs_tenant ON exchange_diffs(tenant_id);
CREATE INDEX idx_exchange_diffs_currency ON exchange_diffs(currency_id);
CREATE INDEX idx_exchange_diffs_period ON exchange_diffs(tenant_id, period_start, period_end);

-- ============================================
-- RECONCILIATION ACTS (Akt sverka)
-- ============================================
CREATE TABLE IF NOT EXISTS reconciliation_acts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    partner_id UUID NOT NULL REFERENCES contacts(id) ON DELETE RESTRICT,
    period_start DATE NOT NULL,
    period_end DATE NOT NULL,
    opening_balance DECIMAL(20, 4) DEFAULT 0,
    our_debit_total DECIMAL(20, 4) DEFAULT 0,
    our_credit_total DECIMAL(20, 4) DEFAULT 0,
    our_balance DECIMAL(20, 4) DEFAULT 0,
    partner_debit_total DECIMAL(20, 4) DEFAULT 0,
    partner_credit_total DECIMAL(20, 4) DEFAULT 0,
    partner_balance DECIMAL(20, 4) DEFAULT 0,
    difference DECIMAL(20, 4) GENERATED ALWAYS AS (our_balance - partner_balance) STORED,
    status VARCHAR(20) NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'sent', 'confirmed', 'disputed')),
    notes TEXT,
    confirmed_at TIMESTAMP WITH TIME ZONE,
    confirmed_by UUID REFERENCES users(id),
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX idx_reconciliation_acts_tenant ON reconciliation_acts(tenant_id);
CREATE INDEX idx_reconciliation_acts_partner ON reconciliation_acts(partner_id);
CREATE INDEX idx_reconciliation_acts_period ON reconciliation_acts(tenant_id, period_start, period_end);
CREATE INDEX idx_reconciliation_acts_status ON reconciliation_acts(tenant_id, status);

-- ============================================
-- RECONCILIATION LINES (Akt sverka qatorlari)
-- ============================================
CREATE TABLE IF NOT EXISTS reconciliation_lines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    act_id UUID NOT NULL REFERENCES reconciliation_acts(id) ON DELETE CASCADE,
    line_date DATE NOT NULL,
    document VARCHAR(255),
    description TEXT,
    our_debit DECIMAL(20, 4) DEFAULT 0,
    our_credit DECIMAL(20, 4) DEFAULT 0,
    partner_debit DECIMAL(20, 4) DEFAULT 0,
    partner_credit DECIMAL(20, 4) DEFAULT 0,
    line_number INTEGER DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_reconciliation_lines_act ON reconciliation_lines(act_id);
CREATE INDEX idx_reconciliation_lines_date ON reconciliation_lines(act_id, line_date);

-- ============================================
-- BUDGET ENHANCEMENTS (Byudjet kengaytirish)
-- ============================================
-- Add columns to existing budgets table if it exists, otherwise create
DO $$
BEGIN
    -- Add budget_type column
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'budgets') THEN
        IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'budgets' AND column_name = 'budget_type') THEN
            ALTER TABLE budgets ADD COLUMN budget_type VARCHAR(30) DEFAULT 'expense' CHECK (budget_type IN ('income', 'expense', 'cashflow', 'investment', 'production'));
        END IF;
        IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'budgets' AND column_name = 'responsible_id') THEN
            ALTER TABLE budgets ADD COLUMN responsible_id UUID REFERENCES users(id);
        END IF;
        IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'budgets' AND column_name = 'warning_threshold') THEN
            ALTER TABLE budgets ADD COLUMN warning_threshold DECIMAL(5, 2) DEFAULT 80.00;
        END IF;
    END IF;
END $$;
