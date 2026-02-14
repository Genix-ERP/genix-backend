-- Migration: 083_recurring_journal_entries.sql
-- Description: Add recurring journal entry templates
-- Date: 2026-02-06

-- 1. Recurring Journal Templates
CREATE TABLE IF NOT EXISTS recurring_journal_templates (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id),
    journal_id UUID NOT NULL REFERENCES journals(id),

    -- Template info
    name VARCHAR(255) NOT NULL,
    description TEXT,
    reference VARCHAR(100),

    -- Recurrence settings
    frequency VARCHAR(20) NOT NULL, -- daily, weekly, monthly, quarterly, yearly
    interval_count INTEGER DEFAULT 1, -- every X days/weeks/months
    day_of_month INTEGER, -- for monthly (1-31)
    day_of_week INTEGER, -- for weekly (0=Sunday, 1=Monday, etc.)
    month_of_year INTEGER, -- for yearly (1-12)

    -- Date range
    start_date DATE NOT NULL,
    end_date DATE, -- NULL = no end date
    next_run_date DATE NOT NULL,
    last_run_date DATE,

    -- Totals (calculated from lines)
    total_debit DECIMAL(20, 4) DEFAULT 0,
    total_credit DECIMAL(20, 4) DEFAULT 0,

    -- Status
    is_active BOOLEAN DEFAULT true,
    auto_post BOOLEAN DEFAULT false, -- auto-post generated entries or leave as draft

    -- Audit
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

-- 2. Recurring Journal Template Lines
CREATE TABLE IF NOT EXISTS recurring_journal_template_lines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    template_id UUID NOT NULL REFERENCES recurring_journal_templates(id) ON DELETE CASCADE,
    line_number INTEGER NOT NULL,
    account_id UUID NOT NULL REFERENCES accounts(id),
    description TEXT,
    debit_amount DECIMAL(20, 4) DEFAULT 0,
    credit_amount DECIMAL(20, 4) DEFAULT 0,
    contact_id UUID REFERENCES contacts(id),
    cost_center_id UUID,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 3. Generated Entries Log (track which entries were generated from templates)
CREATE TABLE IF NOT EXISTS recurring_journal_log (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    template_id UUID NOT NULL REFERENCES recurring_journal_templates(id) ON DELETE CASCADE,
    journal_entry_id UUID NOT NULL REFERENCES journal_entries(id) ON DELETE CASCADE,
    generated_for_date DATE NOT NULL,
    generated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(template_id, generated_for_date)
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_recurring_templates_tenant ON recurring_journal_templates(tenant_id);
CREATE INDEX IF NOT EXISTS idx_recurring_templates_next_run ON recurring_journal_templates(next_run_date) WHERE is_active = true AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_recurring_template_lines_template ON recurring_journal_template_lines(template_id);
CREATE INDEX IF NOT EXISTS idx_recurring_log_template ON recurring_journal_log(template_id);

-- Permissions
INSERT INTO permissions (code, name, description, module, resource, action)
VALUES
    ('recurring_journal:create', 'Create Recurring Journal', 'Can create recurring journal templates', 'finance', 'recurring_journal', 'create'),
    ('recurring_journal:read', 'View Recurring Journal', 'Can view recurring journal templates', 'finance', 'recurring_journal', 'read'),
    ('recurring_journal:update', 'Update Recurring Journal', 'Can update recurring journal templates', 'finance', 'recurring_journal', 'update'),
    ('recurring_journal:delete', 'Delete Recurring Journal', 'Can delete recurring journal templates', 'finance', 'recurring_journal', 'delete'),
    ('recurring_journal:generate', 'Generate Recurring Entries', 'Can manually generate recurring entries', 'finance', 'recurring_journal', 'generate')
ON CONFLICT (code) DO NOTHING;
