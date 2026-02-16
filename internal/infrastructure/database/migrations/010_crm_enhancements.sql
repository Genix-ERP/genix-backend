-- Migration: 008_crm_enhancements.sql
-- Description: Add Opportunities, Activities, Tasks, Campaigns, and other CRM enhancements
-- Date: 2026-01-15

-- =====================================================
-- PIPELINE STAGES TABLE
-- =====================================================

-- 1. Pipeline Stages (for customizable sales pipelines)
CREATE TABLE IF NOT EXISTS pipeline_stages (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    code VARCHAR(50) NOT NULL,
    sequence INTEGER DEFAULT 0,
    probability DECIMAL(5, 2) DEFAULT 0, -- Win probability percentage
    is_won BOOLEAN DEFAULT false,
    is_lost BOOLEAN DEFAULT false,
    color VARCHAR(20) DEFAULT '#3B82F6',
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, code)
);

-- =====================================================
-- OPPORTUNITIES TABLE
-- =====================================================

-- 2. Opportunities (Sales Deals)
CREATE TABLE IF NOT EXISTS opportunities (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    code VARCHAR(50),
    contact_id UUID REFERENCES contacts(id),
    lead_id UUID REFERENCES leads(id), -- If converted from lead
    stage_id UUID REFERENCES pipeline_stages(id),
    stage VARCHAR(50) DEFAULT 'qualification', -- qualification, proposal, negotiation, closed_won, closed_lost
    probability DECIMAL(5, 2) DEFAULT 0,
    expected_revenue DECIMAL(20, 4) DEFAULT 0,
    actual_revenue DECIMAL(20, 4),
    currency VARCHAR(3) DEFAULT 'USD',
    expected_close_date DATE,
    actual_close_date DATE,
    source VARCHAR(50), -- website, referral, cold_call, marketing, partner, other
    priority VARCHAR(20) DEFAULT 'medium', -- low, medium, high, urgent
    assigned_to UUID REFERENCES users(id),
    team_id UUID,
    description TEXT,
    next_step TEXT,
    next_step_date DATE,
    lost_reason VARCHAR(255),
    competitor VARCHAR(255),
    tags JSONB DEFAULT '[]',
    custom_fields JSONB DEFAULT '{}',
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, code)
);

-- =====================================================
-- ACTIVITIES TABLE
-- =====================================================

-- 3. Activities (Calls, Meetings, Emails, Notes)
CREATE TABLE IF NOT EXISTS activities (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    activity_type VARCHAR(50) NOT NULL, -- call, meeting, email, note, task, follow_up
    subject VARCHAR(255) NOT NULL,
    description TEXT,
    -- Polymorphic relationship
    related_type VARCHAR(50), -- lead, contact, opportunity
    related_id UUID,
    -- Specific references
    lead_id UUID REFERENCES leads(id) ON DELETE SET NULL,
    contact_id UUID REFERENCES contacts(id) ON DELETE SET NULL,
    opportunity_id UUID REFERENCES opportunities(id) ON DELETE SET NULL,
    -- Activity details
    start_datetime TIMESTAMP WITH TIME ZONE,
    end_datetime TIMESTAMP WITH TIME ZONE,
    duration_minutes INTEGER,
    location VARCHAR(255),
    is_all_day BOOLEAN DEFAULT false,
    -- Status
    status VARCHAR(20) DEFAULT 'planned', -- planned, in_progress, completed, cancelled
    outcome VARCHAR(50), -- successful, unsuccessful, no_answer, rescheduled
    outcome_notes TEXT,
    -- Participants
    assigned_to UUID REFERENCES users(id),
    attendees JSONB DEFAULT '[]', -- [{user_id, email, name, status}]
    -- Reminders
    reminder_datetime TIMESTAMP WITH TIME ZONE,
    reminder_sent BOOLEAN DEFAULT false,
    -- Metadata
    priority VARCHAR(20) DEFAULT 'medium',
    is_private BOOLEAN DEFAULT false,
    tags JSONB DEFAULT '[]',
    custom_fields JSONB DEFAULT '{}',
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

-- =====================================================
-- TASKS TABLE
-- =====================================================

-- 4. Tasks (To-do items)
CREATE TABLE IF NOT EXISTS crm_tasks (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    -- Related records
    lead_id UUID REFERENCES leads(id) ON DELETE SET NULL,
    contact_id UUID REFERENCES contacts(id) ON DELETE SET NULL,
    opportunity_id UUID REFERENCES opportunities(id) ON DELETE SET NULL,
    activity_id UUID REFERENCES activities(id) ON DELETE SET NULL,
    -- Task details
    task_type VARCHAR(50) DEFAULT 'general', -- general, follow_up, call, email, meeting, review
    priority VARCHAR(20) DEFAULT 'medium', -- low, medium, high, urgent
    status VARCHAR(20) DEFAULT 'pending', -- pending, in_progress, completed, cancelled, deferred
    due_date DATE,
    due_time TIME,
    completed_at TIMESTAMP WITH TIME ZONE,
    -- Assignment
    assigned_to UUID REFERENCES users(id),
    assigned_by UUID REFERENCES users(id),
    -- Progress
    progress_percent INTEGER DEFAULT 0,
    estimated_hours DECIMAL(10, 2),
    actual_hours DECIMAL(10, 2),
    -- Recurrence
    is_recurring BOOLEAN DEFAULT false,
    recurrence_pattern VARCHAR(50), -- daily, weekly, monthly, yearly
    recurrence_end_date DATE,
    parent_task_id UUID REFERENCES crm_tasks(id),
    -- Metadata
    tags JSONB DEFAULT '[]',
    custom_fields JSONB DEFAULT '{}',
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

-- =====================================================
-- CAMPAIGNS TABLE
-- =====================================================

-- 5. Marketing Campaigns
CREATE TABLE IF NOT EXISTS campaigns (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    code VARCHAR(50) NOT NULL,
    campaign_type VARCHAR(50) NOT NULL, -- email, social, ads, event, referral, content, other
    status VARCHAR(20) DEFAULT 'draft', -- draft, scheduled, active, paused, completed, cancelled
    -- Dates
    start_date DATE,
    end_date DATE,
    -- Budget
    budgeted_cost DECIMAL(20, 4) DEFAULT 0,
    actual_cost DECIMAL(20, 4) DEFAULT 0,
    currency VARCHAR(3) DEFAULT 'USD',
    -- Targets
    target_audience TEXT,
    target_leads INTEGER DEFAULT 0,
    target_conversions INTEGER DEFAULT 0,
    -- Results
    actual_leads INTEGER DEFAULT 0,
    actual_conversions INTEGER DEFAULT 0,
    total_revenue DECIMAL(20, 4) DEFAULT 0,
    -- Content
    description TEXT,
    message_template TEXT,
    -- Assignment
    owner_id UUID REFERENCES users(id),
    team_members JSONB DEFAULT '[]',
    -- Metadata
    tags JSONB DEFAULT '[]',
    custom_fields JSONB DEFAULT '{}',
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, code)
);

-- 6. Campaign Members (Leads/Contacts in a campaign)
CREATE TABLE IF NOT EXISTS campaign_members (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    campaign_id UUID NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    lead_id UUID REFERENCES leads(id) ON DELETE CASCADE,
    contact_id UUID REFERENCES contacts(id) ON DELETE CASCADE,
    status VARCHAR(50) DEFAULT 'sent', -- sent, opened, clicked, responded, converted, unsubscribed
    sent_at TIMESTAMP WITH TIME ZONE,
    opened_at TIMESTAMP WITH TIME ZONE,
    clicked_at TIMESTAMP WITH TIME ZONE,
    responded_at TIMESTAMP WITH TIME ZONE,
    converted_at TIMESTAMP WITH TIME ZONE,
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT campaign_member_reference CHECK (lead_id IS NOT NULL OR contact_id IS NOT NULL)
);

-- =====================================================
-- EMAIL INTEGRATION TABLES
-- =====================================================

-- 7. Email Templates
CREATE TABLE IF NOT EXISTS email_templates (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    code VARCHAR(50) NOT NULL,
    subject VARCHAR(500) NOT NULL,
    body_html TEXT,
    body_text TEXT,
    template_type VARCHAR(50) DEFAULT 'general', -- general, follow_up, proposal, welcome, reminder
    variables JSONB DEFAULT '[]', -- Available merge fields
    is_active BOOLEAN DEFAULT true,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, code)
);

-- 8. Email Messages (Sent/Received emails)
CREATE TABLE IF NOT EXISTS email_messages (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    message_id VARCHAR(500), -- Email message ID
    thread_id VARCHAR(500), -- Email thread ID
    direction VARCHAR(10) NOT NULL, -- inbound, outbound
    from_address VARCHAR(255) NOT NULL,
    to_addresses JSONB NOT NULL, -- Array of email addresses
    cc_addresses JSONB DEFAULT '[]',
    bcc_addresses JSONB DEFAULT '[]',
    subject VARCHAR(500),
    body_html TEXT,
    body_text TEXT,
    -- Related records
    lead_id UUID REFERENCES leads(id) ON DELETE SET NULL,
    contact_id UUID REFERENCES contacts(id) ON DELETE SET NULL,
    opportunity_id UUID REFERENCES opportunities(id) ON DELETE SET NULL,
    campaign_id UUID REFERENCES campaigns(id) ON DELETE SET NULL,
    template_id UUID REFERENCES email_templates(id),
    -- Status
    status VARCHAR(20) DEFAULT 'draft', -- draft, queued, sent, delivered, opened, clicked, bounced, failed
    sent_at TIMESTAMP WITH TIME ZONE,
    opened_at TIMESTAMP WITH TIME ZONE,
    clicked_at TIMESTAMP WITH TIME ZONE,
    -- Attachments
    has_attachments BOOLEAN DEFAULT false,
    attachments JSONB DEFAULT '[]', -- [{name, size, type, url}]
    -- Metadata
    headers JSONB DEFAULT '{}',
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- =====================================================
-- QUOTATIONS TABLE
-- =====================================================

-- 9. Quotations/Proposals
CREATE TABLE IF NOT EXISTS quotations (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    quotation_number VARCHAR(50) NOT NULL,
    opportunity_id UUID REFERENCES opportunities(id) ON DELETE SET NULL,
    contact_id UUID REFERENCES contacts(id),
    -- Dates
    quotation_date DATE NOT NULL,
    expiry_date DATE,
    -- Status
    status VARCHAR(20) DEFAULT 'draft', -- draft, sent, viewed, accepted, rejected, expired, converted
    -- Amounts
    subtotal DECIMAL(20, 4) DEFAULT 0,
    discount_amount DECIMAL(20, 4) DEFAULT 0,
    discount_percent DECIMAL(5, 2) DEFAULT 0,
    tax_amount DECIMAL(20, 4) DEFAULT 0,
    total_amount DECIMAL(20, 4) DEFAULT 0,
    currency VARCHAR(3) DEFAULT 'USD',
    -- Content
    title VARCHAR(255),
    introduction TEXT,
    terms_and_conditions TEXT,
    notes TEXT,
    -- Tracking
    sent_at TIMESTAMP WITH TIME ZONE,
    viewed_at TIMESTAMP WITH TIME ZONE,
    accepted_at TIMESTAMP WITH TIME ZONE,
    rejected_at TIMESTAMP WITH TIME ZONE,
    rejection_reason TEXT,
    -- Conversion
    converted_to_order BOOLEAN DEFAULT false,
    order_id UUID,
    -- Assignment
    assigned_to UUID REFERENCES users(id),
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, quotation_number)
);

-- 10. Quotation Lines
CREATE TABLE IF NOT EXISTS quotation_lines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    quotation_id UUID NOT NULL REFERENCES quotations(id) ON DELETE CASCADE,
    line_number INTEGER NOT NULL,
    product_id UUID REFERENCES products(id),
    description VARCHAR(500) NOT NULL,
    quantity DECIMAL(20, 4) NOT NULL DEFAULT 1,
    unit_price DECIMAL(20, 4) NOT NULL DEFAULT 0,
    discount_percent DECIMAL(5, 2) DEFAULT 0,
    discount_amount DECIMAL(20, 4) DEFAULT 0,
    tax_percent DECIMAL(5, 2) DEFAULT 0,
    tax_amount DECIMAL(20, 4) DEFAULT 0,
    line_total DECIMAL(20, 4) DEFAULT 0,
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- =====================================================
-- CUSTOMER SEGMENTATION
-- =====================================================

-- 11. Customer Segments
CREATE TABLE IF NOT EXISTS customer_segments (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    code VARCHAR(50) NOT NULL,
    description TEXT,
    -- Segment criteria (stored as JSON rules)
    criteria JSONB DEFAULT '{}',
    -- Calculated fields
    member_count INTEGER DEFAULT 0,
    last_calculated_at TIMESTAMP WITH TIME ZONE,
    -- Status
    is_dynamic BOOLEAN DEFAULT true, -- Auto-update membership
    is_active BOOLEAN DEFAULT true,
    -- Metadata
    color VARCHAR(20) DEFAULT '#3B82F6',
    tags JSONB DEFAULT '[]',
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, code)
);

-- 12. Segment Members
CREATE TABLE IF NOT EXISTS segment_members (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    segment_id UUID NOT NULL REFERENCES customer_segments(id) ON DELETE CASCADE,
    contact_id UUID NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    added_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    added_by VARCHAR(50) DEFAULT 'auto', -- auto, manual
    score DECIMAL(10, 2), -- Optional scoring for segment
    UNIQUE(segment_id, contact_id)
);

-- =====================================================
-- NOTES AND ATTACHMENTS
-- =====================================================

-- 13. CRM Notes (Generic notes for any CRM entity)
CREATE TABLE IF NOT EXISTS crm_notes (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    title VARCHAR(255),
    content TEXT NOT NULL,
    -- Polymorphic relationship
    related_type VARCHAR(50) NOT NULL, -- lead, contact, opportunity, activity, task
    related_id UUID NOT NULL,
    -- Visibility
    is_private BOOLEAN DEFAULT false,
    is_pinned BOOLEAN DEFAULT false,
    -- Metadata
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

-- 14. CRM Attachments
CREATE TABLE IF NOT EXISTS crm_attachments (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    file_name VARCHAR(255) NOT NULL,
    file_type VARCHAR(100),
    file_size INTEGER,
    file_url TEXT NOT NULL,
    -- Polymorphic relationship
    related_type VARCHAR(50) NOT NULL, -- lead, contact, opportunity, quotation, email
    related_id UUID NOT NULL,
    -- Metadata
    description TEXT,
    uploaded_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- =====================================================
-- LEAD SCORING RULES
-- =====================================================

-- 15. Lead Scoring Rules
CREATE TABLE IF NOT EXISTS lead_scoring_rules (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    -- Rule definition
    field VARCHAR(100) NOT NULL, -- source, industry, company_size, etc.
    operator VARCHAR(20) NOT NULL, -- equals, contains, greater_than, less_than
    value TEXT NOT NULL,
    score INTEGER NOT NULL, -- Points to add/subtract
    -- Status
    is_active BOOLEAN DEFAULT true,
    priority INTEGER DEFAULT 0,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- =====================================================
-- ADD SCORE COLUMN TO LEADS
-- =====================================================

ALTER TABLE leads ADD COLUMN IF NOT EXISTS score INTEGER DEFAULT 0;
ALTER TABLE leads ADD COLUMN IF NOT EXISTS score_details JSONB DEFAULT '{}';

-- =====================================================
-- INDEXES
-- =====================================================

-- Pipeline stages indexes
CREATE INDEX IF NOT EXISTS idx_pipeline_stages_tenant ON pipeline_stages(tenant_id);
CREATE INDEX IF NOT EXISTS idx_pipeline_stages_sequence ON pipeline_stages(sequence);

-- Opportunities indexes
CREATE INDEX IF NOT EXISTS idx_opportunities_tenant ON opportunities(tenant_id);
CREATE INDEX IF NOT EXISTS idx_opportunities_contact ON opportunities(contact_id);
CREATE INDEX IF NOT EXISTS idx_opportunities_lead ON opportunities(lead_id);
CREATE INDEX IF NOT EXISTS idx_opportunities_stage ON opportunities(stage);
CREATE INDEX IF NOT EXISTS idx_opportunities_stage_id ON opportunities(stage_id);
CREATE INDEX IF NOT EXISTS idx_opportunities_assigned ON opportunities(assigned_to);
CREATE INDEX IF NOT EXISTS idx_opportunities_close_date ON opportunities(expected_close_date);
CREATE INDEX IF NOT EXISTS idx_opportunities_deleted ON opportunities(deleted_at) WHERE deleted_at IS NULL;

-- Activities indexes
CREATE INDEX IF NOT EXISTS idx_activities_tenant ON activities(tenant_id);
CREATE INDEX IF NOT EXISTS idx_activities_type ON activities(activity_type);
CREATE INDEX IF NOT EXISTS idx_activities_lead ON activities(lead_id);
CREATE INDEX IF NOT EXISTS idx_activities_contact ON activities(contact_id);
CREATE INDEX IF NOT EXISTS idx_activities_opportunity ON activities(opportunity_id);
CREATE INDEX IF NOT EXISTS idx_activities_assigned ON activities(assigned_to);
CREATE INDEX IF NOT EXISTS idx_activities_start_datetime ON activities(start_datetime);
CREATE INDEX IF NOT EXISTS idx_activities_status ON activities(status);
CREATE INDEX IF NOT EXISTS idx_activities_related ON activities(related_type, related_id);

-- Tasks indexes
CREATE INDEX IF NOT EXISTS idx_crm_tasks_tenant ON crm_tasks(tenant_id);
CREATE INDEX IF NOT EXISTS idx_crm_tasks_lead ON crm_tasks(lead_id);
CREATE INDEX IF NOT EXISTS idx_crm_tasks_contact ON crm_tasks(contact_id);
CREATE INDEX IF NOT EXISTS idx_crm_tasks_opportunity ON crm_tasks(opportunity_id);
CREATE INDEX IF NOT EXISTS idx_crm_tasks_assigned ON crm_tasks(assigned_to);
CREATE INDEX IF NOT EXISTS idx_crm_tasks_status ON crm_tasks(status);
CREATE INDEX IF NOT EXISTS idx_crm_tasks_due_date ON crm_tasks(due_date);
CREATE INDEX IF NOT EXISTS idx_crm_tasks_priority ON crm_tasks(priority);

-- Campaigns indexes
CREATE INDEX IF NOT EXISTS idx_campaigns_tenant ON campaigns(tenant_id);
CREATE INDEX IF NOT EXISTS idx_campaigns_type ON campaigns(campaign_type);
CREATE INDEX IF NOT EXISTS idx_campaigns_status ON campaigns(status);
CREATE INDEX IF NOT EXISTS idx_campaigns_dates ON campaigns(start_date, end_date);
CREATE INDEX IF NOT EXISTS idx_campaign_members_campaign ON campaign_members(campaign_id);
CREATE INDEX IF NOT EXISTS idx_campaign_members_lead ON campaign_members(lead_id);
CREATE INDEX IF NOT EXISTS idx_campaign_members_contact ON campaign_members(contact_id);

-- Email indexes
CREATE INDEX IF NOT EXISTS idx_email_templates_tenant ON email_templates(tenant_id);
CREATE INDEX IF NOT EXISTS idx_email_messages_tenant ON email_messages(tenant_id);
CREATE INDEX IF NOT EXISTS idx_email_messages_lead ON email_messages(lead_id);
CREATE INDEX IF NOT EXISTS idx_email_messages_contact ON email_messages(contact_id);
CREATE INDEX IF NOT EXISTS idx_email_messages_opportunity ON email_messages(opportunity_id);
CREATE INDEX IF NOT EXISTS idx_email_messages_status ON email_messages(status);
CREATE INDEX IF NOT EXISTS idx_email_messages_thread ON email_messages(thread_id);

-- Quotation indexes
CREATE INDEX IF NOT EXISTS idx_quotations_tenant ON quotations(tenant_id);
CREATE INDEX IF NOT EXISTS idx_quotations_opportunity ON quotations(opportunity_id);
CREATE INDEX IF NOT EXISTS idx_quotations_contact ON quotations(contact_id);
CREATE INDEX IF NOT EXISTS idx_quotations_status ON quotations(status);
CREATE INDEX IF NOT EXISTS idx_quotations_date ON quotations(quotation_date);
CREATE INDEX IF NOT EXISTS idx_quotation_lines_quotation ON quotation_lines(quotation_id);

-- Segment indexes
CREATE INDEX IF NOT EXISTS idx_customer_segments_tenant ON customer_segments(tenant_id);
CREATE INDEX IF NOT EXISTS idx_segment_members_segment ON segment_members(segment_id);
CREATE INDEX IF NOT EXISTS idx_segment_members_contact ON segment_members(contact_id);

-- Notes and attachments indexes
CREATE INDEX IF NOT EXISTS idx_crm_notes_tenant ON crm_notes(tenant_id);
CREATE INDEX IF NOT EXISTS idx_crm_notes_related ON crm_notes(related_type, related_id);
CREATE INDEX IF NOT EXISTS idx_crm_attachments_tenant ON crm_attachments(tenant_id);
CREATE INDEX IF NOT EXISTS idx_crm_attachments_related ON crm_attachments(related_type, related_id);

-- Lead scoring indexes
CREATE INDEX IF NOT EXISTS idx_lead_scoring_rules_tenant ON lead_scoring_rules(tenant_id);
CREATE INDEX IF NOT EXISTS idx_leads_score ON leads(score);

-- =====================================================
-- TRIGGERS
-- =====================================================

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_pipeline_stages_updated_at') THEN
        CREATE TRIGGER update_pipeline_stages_updated_at BEFORE UPDATE ON pipeline_stages
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_opportunities_updated_at') THEN
        CREATE TRIGGER update_opportunities_updated_at BEFORE UPDATE ON opportunities
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_activities_updated_at') THEN
        CREATE TRIGGER update_activities_updated_at BEFORE UPDATE ON activities
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_crm_tasks_updated_at') THEN
        CREATE TRIGGER update_crm_tasks_updated_at BEFORE UPDATE ON crm_tasks
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_campaigns_updated_at') THEN
        CREATE TRIGGER update_campaigns_updated_at BEFORE UPDATE ON campaigns
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_campaign_members_updated_at') THEN
        CREATE TRIGGER update_campaign_members_updated_at BEFORE UPDATE ON campaign_members
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_email_templates_updated_at') THEN
        CREATE TRIGGER update_email_templates_updated_at BEFORE UPDATE ON email_templates
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_email_messages_updated_at') THEN
        CREATE TRIGGER update_email_messages_updated_at BEFORE UPDATE ON email_messages
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_quotations_updated_at') THEN
        CREATE TRIGGER update_quotations_updated_at BEFORE UPDATE ON quotations
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_quotation_lines_updated_at') THEN
        CREATE TRIGGER update_quotation_lines_updated_at BEFORE UPDATE ON quotation_lines
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_customer_segments_updated_at') THEN
        CREATE TRIGGER update_customer_segments_updated_at BEFORE UPDATE ON customer_segments
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_crm_notes_updated_at') THEN
        CREATE TRIGGER update_crm_notes_updated_at BEFORE UPDATE ON crm_notes
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_lead_scoring_rules_updated_at') THEN
        CREATE TRIGGER update_lead_scoring_rules_updated_at BEFORE UPDATE ON lead_scoring_rules
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;
END $$;

-- =====================================================
-- SEED DEFAULT PIPELINE STAGES
-- =====================================================

-- Note: These will need to be inserted per tenant
-- INSERT INTO pipeline_stages (tenant_id, name, code, sequence, probability, is_won, is_lost, color) VALUES
-- (tenant_id, 'Qualification', 'qualification', 1, 10, false, false, '#6B7280'),
-- (tenant_id, 'Needs Analysis', 'needs_analysis', 2, 25, false, false, '#3B82F6'),
-- (tenant_id, 'Proposal', 'proposal', 3, 50, false, false, '#8B5CF6'),
-- (tenant_id, 'Negotiation', 'negotiation', 4, 75, false, false, '#F59E0B'),
-- (tenant_id, 'Closed Won', 'closed_won', 5, 100, true, false, '#10B981'),
-- (tenant_id, 'Closed Lost', 'closed_lost', 6, 0, false, true, '#EF4444');
