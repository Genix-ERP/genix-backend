-- =====================================================
-- ACTIVITY LOG SYSTEM (Odoo-style Chatter)
-- Migration: 111_activity_log.sql
-- =====================================================

-- Activity Log table - tracks all changes and comments on records
CREATE TABLE IF NOT EXISTS activity_logs (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),

    -- What record this activity is about
    model_name VARCHAR(100) NOT NULL,  -- e.g., 'construction_project', 'sales_order'
    record_id BIGINT NOT NULL,          -- ID of the record

    -- Activity details
    activity_type VARCHAR(50) NOT NULL, -- 'create', 'update', 'delete', 'comment', 'status_change', 'assignment'
    title VARCHAR(500),                  -- Short description
    description TEXT,                    -- Detailed description or comment

    -- For field changes
    field_changes JSONB,                 -- {"field_name": {"old": "value1", "new": "value2"}}

    -- For status changes
    old_status VARCHAR(100),
    new_status VARCHAR(100),

    -- Who did it
    user_id UUID REFERENCES users(id),
    user_name VARCHAR(255),              -- Cached for display

    -- When
    created_at TIMESTAMP DEFAULT NOW(),

    -- Optional: link to related records
    related_model VARCHAR(100),
    related_id BIGINT
);

-- Indexes for fast lookups
CREATE INDEX IF NOT EXISTS idx_activity_logs_tenant ON activity_logs(tenant_id);
CREATE INDEX IF NOT EXISTS idx_activity_logs_model_record ON activity_logs(model_name, record_id);
CREATE INDEX IF NOT EXISTS idx_activity_logs_user ON activity_logs(user_id);
CREATE INDEX IF NOT EXISTS idx_activity_logs_created ON activity_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_activity_logs_type ON activity_logs(activity_type);

-- Comments table - for threaded discussions on records
CREATE TABLE IF NOT EXISTS record_comments (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),

    -- What record this comment is on
    model_name VARCHAR(100) NOT NULL,
    record_id BIGINT NOT NULL,

    -- Comment content
    content TEXT NOT NULL,

    -- Threading
    parent_id BIGINT REFERENCES record_comments(id),

    -- Mentions (users tagged in comment)
    mentions JSONB,  -- [{"user_id": "uuid", "name": "John"}]

    -- Attachments
    attachments JSONB,  -- [{"file_id": "uuid", "name": "doc.pdf", "url": "..."}]

    -- Who and when
    user_id UUID REFERENCES users(id),
    user_name VARCHAR(255),
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    is_deleted BOOLEAN DEFAULT FALSE
);

CREATE INDEX IF NOT EXISTS idx_record_comments_model_record ON record_comments(model_name, record_id);
CREATE INDEX IF NOT EXISTS idx_record_comments_parent ON record_comments(parent_id);

-- Scheduled Activities (like Odoo's planned activities)
CREATE TABLE IF NOT EXISTS scheduled_activities (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),

    -- What record
    model_name VARCHAR(100) NOT NULL,
    record_id BIGINT NOT NULL,

    -- Activity details
    activity_type VARCHAR(50) NOT NULL,  -- 'call', 'meeting', 'email', 'todo', 'reminder'
    summary VARCHAR(500) NOT NULL,
    note TEXT,

    -- Due date
    due_date DATE NOT NULL,
    due_time TIME,

    -- Assignment
    assigned_to UUID REFERENCES users(id),
    assigned_by UUID REFERENCES users(id),

    -- Status
    status VARCHAR(50) DEFAULT 'pending',  -- 'pending', 'done', 'cancelled'
    completed_at TIMESTAMP,
    completed_by UUID REFERENCES users(id),

    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_scheduled_activities_model_record ON scheduled_activities(model_name, record_id);
CREATE INDEX IF NOT EXISTS idx_scheduled_activities_assigned ON scheduled_activities(assigned_to);
CREATE INDEX IF NOT EXISTS idx_scheduled_activities_due ON scheduled_activities(due_date);
CREATE INDEX IF NOT EXISTS idx_scheduled_activities_status ON scheduled_activities(status);
