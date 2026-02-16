-- Project Team Members table
-- Drop existing table if it was partially created
DROP TABLE IF EXISTS project_team_members CASCADE;

CREATE TABLE project_team_members (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    employee_name VARCHAR(255) NOT NULL,
    role VARCHAR(100),
    allocation_percent INTEGER DEFAULT 100,
    start_date DATE,
    end_date DATE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(project_id, employee_id)
);

CREATE INDEX idx_project_team_project ON project_team_members(project_id);
CREATE INDEX idx_project_team_employee ON project_team_members(employee_id);
CREATE INDEX idx_project_team_tenant ON project_team_members(tenant_id);
