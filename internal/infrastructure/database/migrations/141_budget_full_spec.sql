-- Migration 141: Full Budget Module Spec Implementation
-- Adds fields required by the Genix ERP Budget Module UX Specification

-- Budget approach (planning method)
ALTER TABLE budgets ADD COLUMN IF NOT EXISTS approach VARCHAR(50) DEFAULT 'fixed';
-- Values: fixed, flexible, zero_based, rolling

-- Rolling horizon (months) for rolling approach
ALTER TABLE budgets ADD COLUMN IF NOT EXISTS rolling_horizon_months INT DEFAULT 3;

-- Auto-extend rolling budget
ALTER TABLE budgets ADD COLUMN IF NOT EXISTS auto_extend BOOLEAN DEFAULT TRUE;

-- Period breakdown granularity
ALTER TABLE budgets ADD COLUMN IF NOT EXISTS breakdown VARCHAR(20) DEFAULT 'monthly';
-- Values: monthly, weekly, none

-- Responsible user
ALTER TABLE budgets ADD COLUMN IF NOT EXISTS responsible_user_id UUID REFERENCES users(id) ON DELETE SET NULL;

-- Department scope
ALTER TABLE budgets ADD COLUMN IF NOT EXISTS department_id UUID;

-- Overspend policy
ALTER TABLE budgets ADD COLUMN IF NOT EXISTS overspend_policy VARCHAR(30) DEFAULT 'warn';
-- Values: warn, require_approval, block

-- Approval workflow
ALTER TABLE budgets ADD COLUMN IF NOT EXISTS approval_status VARCHAR(30) DEFAULT NULL;
-- Values: pending, approved, rejected
ALTER TABLE budgets ADD COLUMN IF NOT EXISTS submitted_by UUID REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE budgets ADD COLUMN IF NOT EXISTS submitted_at TIMESTAMPTZ;
ALTER TABLE budgets ADD COLUMN IF NOT EXISTS rejection_reason TEXT;

-- Budget category (for grouping lines - BDR vs CAPEX etc)
ALTER TABLE budgets ADD COLUMN IF NOT EXISTS category VARCHAR(50) DEFAULT NULL;

-- Budget lines: add monthly breakdown columns (period_1 to period_12)
ALTER TABLE budget_lines ADD COLUMN IF NOT EXISTS period_1 DECIMAL(20, 4) DEFAULT 0;
ALTER TABLE budget_lines ADD COLUMN IF NOT EXISTS period_2 DECIMAL(20, 4) DEFAULT 0;
ALTER TABLE budget_lines ADD COLUMN IF NOT EXISTS period_3 DECIMAL(20, 4) DEFAULT 0;
ALTER TABLE budget_lines ADD COLUMN IF NOT EXISTS period_4 DECIMAL(20, 4) DEFAULT 0;
ALTER TABLE budget_lines ADD COLUMN IF NOT EXISTS period_5 DECIMAL(20, 4) DEFAULT 0;
ALTER TABLE budget_lines ADD COLUMN IF NOT EXISTS period_6 DECIMAL(20, 4) DEFAULT 0;
ALTER TABLE budget_lines ADD COLUMN IF NOT EXISTS period_7 DECIMAL(20, 4) DEFAULT 0;
ALTER TABLE budget_lines ADD COLUMN IF NOT EXISTS period_8 DECIMAL(20, 4) DEFAULT 0;
ALTER TABLE budget_lines ADD COLUMN IF NOT EXISTS period_9 DECIMAL(20, 4) DEFAULT 0;
ALTER TABLE budget_lines ADD COLUMN IF NOT EXISTS period_10 DECIMAL(20, 4) DEFAULT 0;
ALTER TABLE budget_lines ADD COLUMN IF NOT EXISTS period_11 DECIMAL(20, 4) DEFAULT 0;
ALTER TABLE budget_lines ADD COLUMN IF NOT EXISTS period_12 DECIMAL(20, 4) DEFAULT 0;

-- Budget lines: flexible budget formula
ALTER TABLE budget_lines ADD COLUMN IF NOT EXISTS formula_type VARCHAR(30) DEFAULT NULL;
-- Values: fixed, percent_of_revenue, percent_of_expense
ALTER TABLE budget_lines ADD COLUMN IF NOT EXISTS formula_value DECIMAL(10, 4) DEFAULT NULL;
ALTER TABLE budget_lines ADD COLUMN IF NOT EXISTS formula_cap DECIMAL(20, 4) DEFAULT NULL;

-- Budget lines: zero-based justification
ALTER TABLE budget_lines ADD COLUMN IF NOT EXISTS justification TEXT DEFAULT NULL;

-- Budget lines: line type for grouping
ALTER TABLE budget_lines ADD COLUMN IF NOT EXISTS line_type VARCHAR(30) DEFAULT 'expense';
-- Values: revenue, expense, investment

-- Budget lines: category name for display
ALTER TABLE budget_lines ADD COLUMN IF NOT EXISTS category_name VARCHAR(200) DEFAULT NULL;

-- Budget categories table (linking user-visible names to accounts)
CREATE TABLE IF NOT EXISTS budget_categories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    name VARCHAR(200) NOT NULL,
    parent_id UUID REFERENCES budget_categories(id) ON DELETE SET NULL,
    line_type VARCHAR(30) NOT NULL DEFAULT 'expense',
    -- Values: revenue, expense, investment
    is_active BOOLEAN DEFAULT TRUE,
    sort_order INT DEFAULT 0,
    default_formula TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Link between budget categories and chart of accounts
CREATE TABLE IF NOT EXISTS budget_category_accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    category_id UUID NOT NULL REFERENCES budget_categories(id) ON DELETE CASCADE,
    account_id UUID NOT NULL,
    tenant_id UUID NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_budget_categories_tenant ON budget_categories(tenant_id);
CREATE INDEX IF NOT EXISTS idx_budget_category_accounts_category ON budget_category_accounts(category_id);
CREATE INDEX IF NOT EXISTS idx_budgets_approach ON budgets(approach);
CREATE INDEX IF NOT EXISTS idx_budgets_approval_status ON budgets(approval_status);
