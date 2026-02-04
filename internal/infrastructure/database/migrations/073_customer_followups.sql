-- Customer Follow-ups (Dunning) Module Migration
-- Automated payment reminders and collection management

-- Follow-up levels (e.g., First Reminder, Second Reminder, Final Notice)
CREATE TABLE IF NOT EXISTS followup_levels (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    name VARCHAR(255) NOT NULL,
    sequence INTEGER NOT NULL DEFAULT 1,

    -- Timing
    delay_days INTEGER NOT NULL DEFAULT 0, -- Days after due date to trigger

    -- Actions
    send_email BOOLEAN DEFAULT true,
    send_sms BOOLEAN DEFAULT false,
    send_letter BOOLEAN DEFAULT false,

    -- Email template
    email_subject VARCHAR(500),
    email_body TEXT,

    -- SMS template
    sms_body VARCHAR(500),

    -- Letter template
    letter_body TEXT,

    -- Escalation
    block_sales BOOLEAN DEFAULT false, -- Block new sales orders
    add_late_fee BOOLEAN DEFAULT false,
    late_fee_type VARCHAR(20), -- percentage, fixed
    late_fee_value DECIMAL(15,2) DEFAULT 0,

    is_active BOOLEAN DEFAULT true,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(tenant_id, sequence)
);

-- Customer follow-up status tracking
CREATE TABLE IF NOT EXISTS customer_followup_status (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    customer_id UUID NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,

    -- Current follow-up state
    current_level_id UUID REFERENCES followup_levels(id),
    current_level_sequence INTEGER DEFAULT 0,

    -- Amounts
    total_overdue DECIMAL(15,2) DEFAULT 0,
    oldest_due_date DATE,
    days_overdue INTEGER DEFAULT 0,

    -- Status
    status VARCHAR(30) DEFAULT 'ok', -- ok, in_followup, blocked

    -- Last actions
    last_followup_date TIMESTAMP WITH TIME ZONE,
    next_followup_date TIMESTAMP WITH TIME ZONE,

    -- Notes
    internal_notes TEXT,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(tenant_id, customer_id)
);

-- Follow-up action history
CREATE TABLE IF NOT EXISTS followup_actions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    customer_id UUID NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,

    -- Follow-up level that triggered this action
    level_id UUID REFERENCES followup_levels(id),
    level_name VARCHAR(255),

    -- Action type
    action_type VARCHAR(30) NOT NULL, -- email_sent, sms_sent, letter_sent, manual_call, note, payment_promise, level_escalation

    -- Related invoices
    invoice_ids UUID[],
    total_amount DECIMAL(15,2),

    -- Action details
    description TEXT,

    -- For email/sms
    recipient VARCHAR(255),
    subject VARCHAR(500),
    body TEXT,

    -- Status
    status VARCHAR(30) DEFAULT 'sent', -- sent, delivered, failed, scheduled
    error_message TEXT,

    -- Performed by
    performed_by UUID REFERENCES users(id),
    performed_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Payment promises from customers
CREATE TABLE IF NOT EXISTS payment_promises (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    customer_id UUID NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,

    -- Promise details
    promised_date DATE NOT NULL,
    promised_amount DECIMAL(15,2) NOT NULL,

    -- Related invoices (optional)
    invoice_ids UUID[],

    -- Status
    status VARCHAR(30) DEFAULT 'pending', -- pending, kept, broken, cancelled

    -- Notes
    notes TEXT,

    -- Tracking
    created_by UUID REFERENCES users(id),
    resolved_at TIMESTAMP WITH TIME ZONE,
    resolved_by UUID REFERENCES users(id),

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Follow-up reports/batches
CREATE TABLE IF NOT EXISTS followup_reports (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Report info
    report_date DATE NOT NULL DEFAULT CURRENT_DATE,

    -- Summary
    total_customers INTEGER DEFAULT 0,
    total_overdue_amount DECIMAL(15,2) DEFAULT 0,

    -- Actions taken
    emails_sent INTEGER DEFAULT 0,
    sms_sent INTEGER DEFAULT 0,
    letters_generated INTEGER DEFAULT 0,

    -- Status
    status VARCHAR(30) DEFAULT 'draft', -- draft, processing, completed

    generated_by UUID REFERENCES users(id),

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMP WITH TIME ZONE
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_followup_levels_tenant ON followup_levels(tenant_id);
CREATE INDEX IF NOT EXISTS idx_followup_levels_sequence ON followup_levels(sequence);
CREATE INDEX IF NOT EXISTS idx_followup_levels_active ON followup_levels(is_active);

CREATE INDEX IF NOT EXISTS idx_customer_followup_tenant ON customer_followup_status(tenant_id);
CREATE INDEX IF NOT EXISTS idx_customer_followup_customer ON customer_followup_status(customer_id);
CREATE INDEX IF NOT EXISTS idx_customer_followup_status ON customer_followup_status(status);
CREATE INDEX IF NOT EXISTS idx_customer_followup_next ON customer_followup_status(next_followup_date);

CREATE INDEX IF NOT EXISTS idx_followup_actions_tenant ON followup_actions(tenant_id);
CREATE INDEX IF NOT EXISTS idx_followup_actions_customer ON followup_actions(customer_id);
CREATE INDEX IF NOT EXISTS idx_followup_actions_type ON followup_actions(action_type);
CREATE INDEX IF NOT EXISTS idx_followup_actions_date ON followup_actions(performed_at);

CREATE INDEX IF NOT EXISTS idx_payment_promises_tenant ON payment_promises(tenant_id);
CREATE INDEX IF NOT EXISTS idx_payment_promises_customer ON payment_promises(customer_id);
CREATE INDEX IF NOT EXISTS idx_payment_promises_status ON payment_promises(status);
CREATE INDEX IF NOT EXISTS idx_payment_promises_date ON payment_promises(promised_date);

CREATE INDEX IF NOT EXISTS idx_followup_reports_tenant ON followup_reports(tenant_id);
CREATE INDEX IF NOT EXISTS idx_followup_reports_date ON followup_reports(report_date);

-- Add follow-up permissions
INSERT INTO permissions (id, module, resource, action, description)
VALUES
    (uuid_generate_v4(), 'finance', 'followup', 'read', 'View customer follow-ups'),
    (uuid_generate_v4(), 'finance', 'followup', 'create', 'Create follow-up actions'),
    (uuid_generate_v4(), 'finance', 'followup', 'update', 'Update follow-up settings'),
    (uuid_generate_v4(), 'finance', 'followup', 'delete', 'Delete follow-up records'),
    (uuid_generate_v4(), 'finance', 'followup_level', 'read', 'View follow-up levels'),
    (uuid_generate_v4(), 'finance', 'followup_level', 'create', 'Create follow-up levels'),
    (uuid_generate_v4(), 'finance', 'followup_level', 'update', 'Update follow-up levels'),
    (uuid_generate_v4(), 'finance', 'followup_level', 'delete', 'Delete follow-up levels')
ON CONFLICT (module, resource, action) DO NOTHING;

-- Insert default follow-up levels
INSERT INTO followup_levels (id, tenant_id, name, sequence, delay_days, send_email, email_subject, email_body, is_active)
SELECT
    uuid_generate_v4(),
    t.id,
    'First Reminder',
    1,
    7,
    true,
    'Payment Reminder - Invoice Overdue',
    E'Dear {customer_name},\n\nThis is a friendly reminder that you have an outstanding balance of {total_amount} that is now {days_overdue} days past due.\n\nPlease arrange payment at your earliest convenience.\n\nBest regards,\n{company_name}',
    true
FROM tenants t
WHERE NOT EXISTS (SELECT 1 FROM followup_levels WHERE tenant_id = t.id AND sequence = 1);

INSERT INTO followup_levels (id, tenant_id, name, sequence, delay_days, send_email, email_subject, email_body, is_active)
SELECT
    uuid_generate_v4(),
    t.id,
    'Second Reminder',
    2,
    14,
    true,
    'Second Payment Reminder - Immediate Attention Required',
    E'Dear {customer_name},\n\nWe noticed that payment of {total_amount} is still outstanding and now {days_overdue} days past due.\n\nPlease contact us immediately to discuss payment arrangements.\n\nBest regards,\n{company_name}',
    true
FROM tenants t
WHERE NOT EXISTS (SELECT 1 FROM followup_levels WHERE tenant_id = t.id AND sequence = 2);

INSERT INTO followup_levels (id, tenant_id, name, sequence, delay_days, send_email, send_letter, email_subject, email_body, block_sales, is_active)
SELECT
    uuid_generate_v4(),
    t.id,
    'Final Notice',
    3,
    30,
    true,
    true,
    'FINAL NOTICE - Account Overdue',
    E'Dear {customer_name},\n\nDespite our previous reminders, your account remains unpaid with a balance of {total_amount}, now {days_overdue} days overdue.\n\nPlease note that failure to pay may result in further action and suspension of services.\n\nContact us immediately.\n\n{company_name}',
    true,
    true
FROM tenants t
WHERE NOT EXISTS (SELECT 1 FROM followup_levels WHERE tenant_id = t.id AND sequence = 3);
