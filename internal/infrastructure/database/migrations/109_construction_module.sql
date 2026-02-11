-- =====================================================
-- CONSTRUCTION MODULE - SMETA (ESTIMATES) MANAGEMENT
-- Migration: 109_construction_module.sql
-- =====================================================

-- 1. Construction Projects (Main project entity)
CREATE TABLE IF NOT EXISTS construction_projects (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    organization_id UUID REFERENCES organizations(id),

    -- Basic Info
    code VARCHAR(50) NOT NULL,
    name VARCHAR(500) NOT NULL,
    description TEXT,

    -- Location
    address TEXT,
    city VARCHAR(255),
    district VARCHAR(255),
    region VARCHAR(255),
    coordinates JSONB,

    -- Client Info
    client_name VARCHAR(500),
    client_contact VARCHAR(255),
    client_phone VARCHAR(50),

    -- Project Details
    project_type VARCHAR(50),
    building_type VARCHAR(100),
    total_area DECIMAL(15,2),
    floors_count INTEGER,

    -- Financial
    contract_amount DECIMAL(18,2),
    currency VARCHAR(3) DEFAULT 'UZS',

    -- Dates
    contract_date DATE,
    planned_start_date DATE,
    planned_end_date DATE,
    actual_start_date DATE,
    actual_end_date DATE,

    -- Status
    status VARCHAR(50) DEFAULT 'draft',
    progress_percent DECIMAL(5,2) DEFAULT 0,

    -- Responsible persons
    project_manager_id BIGINT REFERENCES employees(id),
    chief_engineer_id BIGINT REFERENCES employees(id),

    -- Metadata
    created_by UUID REFERENCES users(id),
    created_date TIMESTAMP DEFAULT NOW(),
    updated_date TIMESTAMP DEFAULT NOW(),
    deleted_at TIMESTAMP,

    UNIQUE(tenant_id, organization_id, code)
);

-- 2. Smeta Sections (Bo'limlar - АР, КЖ, Инженерный, etc.)
CREATE TABLE IF NOT EXISTS smeta_sections (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    project_id BIGINT NOT NULL REFERENCES construction_projects(id) ON DELETE CASCADE,
    parent_id BIGINT REFERENCES smeta_sections(id),

    code VARCHAR(50) NOT NULL,
    name VARCHAR(500) NOT NULL,
    name_uz VARCHAR(500),
    description TEXT,

    -- Calculated totals
    total_labor_hours DECIMAL(15,2) DEFAULT 0,
    total_labor_cost DECIMAL(18,2) DEFAULT 0,
    total_material_cost DECIMAL(18,2) DEFAULT 0,
    total_equipment_cost DECIMAL(18,2) DEFAULT 0,
    total_overhead_cost DECIMAL(18,2) DEFAULT 0,
    total_cost DECIMAL(18,2) DEFAULT 0,

    sort_order INTEGER DEFAULT 0,
    status VARCHAR(50) DEFAULT 'draft',

    created_date TIMESTAMP DEFAULT NOW(),
    updated_date TIMESTAMP DEFAULT NOW(),

    UNIQUE(project_id, code)
);

-- 3. Smeta Items (Smeta qatorlari - individual work items)
CREATE TABLE IF NOT EXISTS smeta_items (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    section_id BIGINT NOT NULL REFERENCES smeta_sections(id) ON DELETE CASCADE,

    -- Item identification
    code VARCHAR(100),
    snip_code VARCHAR(100),

    -- Description
    name VARCHAR(1000) NOT NULL,
    name_uz VARCHAR(1000),
    unit VARCHAR(50) NOT NULL,

    -- Quantities
    quantity DECIMAL(15,4) NOT NULL,
    completed_quantity DECIMAL(15,4) DEFAULT 0,

    -- Pricing
    unit_price DECIMAL(18,4),
    total_price DECIMAL(18,2),

    -- Cost breakdown
    labor_hours DECIMAL(15,2) DEFAULT 0,
    labor_cost DECIMAL(18,2) DEFAULT 0,
    material_cost DECIMAL(18,2) DEFAULT 0,
    equipment_cost DECIMAL(18,2) DEFAULT 0,
    transport_cost DECIMAL(18,2) DEFAULT 0,
    overhead_cost DECIMAL(18,2) DEFAULT 0,

    -- Status
    status VARCHAR(50) DEFAULT 'pending',
    progress_percent DECIMAL(5,2) DEFAULT 0,

    -- Notes
    notes TEXT,
    technical_specs JSONB,

    sort_order INTEGER DEFAULT 0,
    created_date TIMESTAMP DEFAULT NOW(),
    updated_date TIMESTAMP DEFAULT NOW()
);

-- 4. Smeta Resources (Materials, labor, equipment for each item)
CREATE TABLE IF NOT EXISTS smeta_resources (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    smeta_item_id BIGINT NOT NULL REFERENCES smeta_items(id) ON DELETE CASCADE,

    resource_type VARCHAR(50) NOT NULL,

    -- Resource identification
    code VARCHAR(100),
    name VARCHAR(500) NOT NULL,
    name_uz VARCHAR(500),

    -- Link to inventory product (if material)
    product_id BIGINT REFERENCES products(id),

    -- Quantities
    unit VARCHAR(50) NOT NULL,
    quantity_per_unit DECIMAL(15,6),
    total_quantity DECIMAL(15,4),
    consumed_quantity DECIMAL(15,4) DEFAULT 0,

    -- Pricing
    unit_price DECIMAL(18,4),
    total_price DECIMAL(18,2),

    notes TEXT,
    created_date TIMESTAMP DEFAULT NOW(),
    updated_date TIMESTAMP DEFAULT NOW()
);

-- 5. Work Progress (KS-2 style - completed work acts)
CREATE TABLE IF NOT EXISTS construction_work_progress (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    project_id BIGINT NOT NULL REFERENCES construction_projects(id),
    smeta_item_id BIGINT NOT NULL REFERENCES smeta_items(id),

    -- Progress details
    report_number VARCHAR(50),
    report_date DATE NOT NULL,
    period_start DATE,
    period_end DATE,

    -- Completed work
    quantity_completed DECIMAL(15,4) NOT NULL,
    unit_price DECIMAL(18,4),
    total_amount DECIMAL(18,2),

    -- Verification
    verified_by BIGINT REFERENCES employees(id),
    verification_date TIMESTAMP,
    verification_notes TEXT,

    status VARCHAR(50) DEFAULT 'draft',

    created_by UUID REFERENCES users(id),
    created_date TIMESTAMP DEFAULT NOW(),
    updated_date TIMESTAMP DEFAULT NOW()
);

-- 6. Photo Reports (Foto hisobotlar)
CREATE TABLE IF NOT EXISTS construction_photo_reports (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    project_id BIGINT NOT NULL REFERENCES construction_projects(id),
    smeta_item_id BIGINT REFERENCES smeta_items(id),
    section_id BIGINT REFERENCES smeta_sections(id),

    -- Report info
    report_date DATE NOT NULL,
    report_type VARCHAR(50),
    title VARCHAR(500),
    description TEXT,

    -- Location
    location_description VARCHAR(500),
    gps_latitude DECIMAL(10,8),
    gps_longitude DECIMAL(11,8),

    -- Weather conditions
    weather VARCHAR(100),
    temperature DECIMAL(5,2),

    -- Photos stored as JSON array
    photos JSONB NOT NULL DEFAULT '[]',

    -- Prorab info
    reported_by BIGINT REFERENCES employees(id),

    -- Review
    reviewed_by BIGINT REFERENCES employees(id),
    review_date TIMESTAMP,
    review_status VARCHAR(50) DEFAULT 'pending',
    review_notes TEXT,

    created_date TIMESTAMP DEFAULT NOW(),
    updated_date TIMESTAMP DEFAULT NOW()
);

-- 7. Daily Reports (Kundalik hisobotlar)
CREATE TABLE IF NOT EXISTS construction_daily_reports (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    project_id BIGINT NOT NULL REFERENCES construction_projects(id),

    report_date DATE NOT NULL,

    -- Weather
    weather_morning VARCHAR(100),
    weather_afternoon VARCHAR(100),
    temperature_min DECIMAL(5,2),
    temperature_max DECIMAL(5,2),

    -- Work summary
    work_summary TEXT,
    issues_encountered TEXT,
    safety_notes TEXT,

    -- Labor
    workers_count INTEGER DEFAULT 0,
    workers_details JSONB,

    -- Equipment
    equipment_used JSONB,

    -- Materials received
    materials_received JSONB,

    -- Visitors/Inspections
    visitors JSONB,

    -- Reported by prorab
    reported_by BIGINT REFERENCES employees(id),

    -- Verification
    verified_by BIGINT REFERENCES employees(id),
    verification_status VARCHAR(50) DEFAULT 'pending',

    created_date TIMESTAMP DEFAULT NOW(),
    updated_date TIMESTAMP DEFAULT NOW(),

    UNIQUE(project_id, report_date)
);

-- 8. Material Requests (Material so'rovlari)
CREATE TABLE IF NOT EXISTS construction_material_requests (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    project_id BIGINT NOT NULL REFERENCES construction_projects(id),

    request_number VARCHAR(50) NOT NULL,
    request_date DATE NOT NULL,
    required_date DATE,

    -- Requestor
    requested_by BIGINT REFERENCES employees(id),

    -- Items
    items JSONB NOT NULL DEFAULT '[]',

    -- Approval
    status VARCHAR(50) DEFAULT 'draft',
    approved_by BIGINT REFERENCES employees(id),
    approval_date TIMESTAMP,
    approval_notes TEXT,

    -- Fulfillment
    fulfilled_date DATE,
    fulfillment_notes TEXT,

    -- Link to purchase order if created
    purchase_order_id BIGINT REFERENCES purchase_orders(id),

    notes TEXT,
    created_date TIMESTAMP DEFAULT NOW(),
    updated_date TIMESTAMP DEFAULT NOW()
);

-- 9. Project Team Members
CREATE TABLE IF NOT EXISTS construction_project_team (
    id BIGSERIAL PRIMARY KEY,
    project_id BIGINT NOT NULL REFERENCES construction_projects(id) ON DELETE CASCADE,
    employee_id BIGINT NOT NULL REFERENCES employees(id),

    role VARCHAR(100) NOT NULL,
    responsibilities TEXT,

    start_date DATE,
    end_date DATE,
    is_active BOOLEAN DEFAULT true,

    created_date TIMESTAMP DEFAULT NOW(),

    UNIQUE(project_id, employee_id, role)
);

-- 10. Cost Summary View (Plan vs Actual)
CREATE TABLE IF NOT EXISTS construction_cost_tracking (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    project_id BIGINT NOT NULL REFERENCES construction_projects(id),
    section_id BIGINT REFERENCES smeta_sections(id),
    smeta_item_id BIGINT REFERENCES smeta_items(id),

    tracking_date DATE NOT NULL,

    -- Planned (from Smeta)
    planned_quantity DECIMAL(15,4),
    planned_cost DECIMAL(18,2),

    -- Actual
    actual_quantity DECIMAL(15,4),
    actual_cost DECIMAL(18,2),

    -- Variance
    quantity_variance DECIMAL(15,4),
    cost_variance DECIMAL(18,2),
    variance_percent DECIMAL(8,2),

    -- Alert threshold exceeded?
    is_over_budget BOOLEAN DEFAULT false,
    alert_sent BOOLEAN DEFAULT false,

    notes TEXT,
    created_date TIMESTAMP DEFAULT NOW()
);

-- 11. Project Vendors/Subcontractors
CREATE TABLE IF NOT EXISTS construction_project_vendors (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    project_id BIGINT NOT NULL REFERENCES construction_projects(id) ON DELETE CASCADE,

    -- Link to existing organization (vendor/supplier)
    vendor_id BIGINT NOT NULL REFERENCES organizations(id),

    -- Contract details
    contract_number VARCHAR(100),
    contract_date DATE,
    contract_amount DECIMAL(18,2),
    currency VARCHAR(3) DEFAULT 'UZS',

    -- What they provide
    vendor_type VARCHAR(50) NOT NULL,
    work_scope TEXT,
    smeta_sections JSONB,

    -- Contact for this project
    contact_person VARCHAR(255),
    contact_phone VARCHAR(50),
    contact_email VARCHAR(255),

    -- Status
    status VARCHAR(50) DEFAULT 'active',

    -- Financial tracking
    total_ordered DECIMAL(18,2) DEFAULT 0,
    total_received DECIMAL(18,2) DEFAULT 0,
    total_invoiced DECIMAL(18,2) DEFAULT 0,
    total_paid DECIMAL(18,2) DEFAULT 0,
    balance_due DECIMAL(18,2) DEFAULT 0,

    start_date DATE,
    end_date DATE,

    notes TEXT,
    created_date TIMESTAMP DEFAULT NOW(),
    updated_date TIMESTAMP DEFAULT NOW(),

    UNIQUE(project_id, vendor_id)
);

-- 12. Material Deliveries
CREATE TABLE IF NOT EXISTS construction_material_deliveries (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    project_id BIGINT NOT NULL REFERENCES construction_projects(id),
    vendor_id BIGINT NOT NULL REFERENCES construction_project_vendors(id),

    -- Delivery info
    delivery_number VARCHAR(50) NOT NULL,
    delivery_date DATE NOT NULL,

    -- Link to procurement system
    purchase_order_id BIGINT REFERENCES purchase_orders(id),
    goods_receipt_id BIGINT REFERENCES goods_receipts(id),

    -- Delivery details
    vehicle_number VARCHAR(50),
    driver_name VARCHAR(255),
    waybill_number VARCHAR(100),

    -- Items delivered
    items JSONB NOT NULL DEFAULT '[]',

    total_amount DECIMAL(18,2),

    -- Received at site by
    received_by BIGINT REFERENCES employees(id),
    received_date TIMESTAMP,

    -- Quality inspection
    quality_status VARCHAR(50) DEFAULT 'pending',
    quality_notes TEXT,
    quality_checked_by BIGINT REFERENCES employees(id),

    -- Photos of delivery
    photos JSONB DEFAULT '[]',

    status VARCHAR(50) DEFAULT 'pending',

    notes TEXT,
    created_date TIMESTAMP DEFAULT NOW(),
    updated_date TIMESTAMP DEFAULT NOW()
);

-- 13. Material Issues
CREATE TABLE IF NOT EXISTS construction_material_issues (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    project_id BIGINT NOT NULL REFERENCES construction_projects(id),

    -- Issue info
    issue_number VARCHAR(50) NOT NULL,
    issue_date DATE NOT NULL,

    -- What smeta item is this for
    smeta_item_id BIGINT REFERENCES smeta_items(id),
    section_id BIGINT REFERENCES smeta_sections(id),

    -- Items issued from warehouse
    items JSONB NOT NULL DEFAULT '[]',

    -- Issued to which worker/team
    issued_to BIGINT REFERENCES employees(id),
    work_location VARCHAR(255),

    -- Approved by
    approved_by BIGINT REFERENCES employees(id),

    -- Return tracking
    returned_items JSONB DEFAULT '[]',

    status VARCHAR(50) DEFAULT 'issued',

    notes TEXT,
    created_date TIMESTAMP DEFAULT NOW(),
    updated_date TIMESTAMP DEFAULT NOW()
);

-- 14. Vendor Payment Schedule
CREATE TABLE IF NOT EXISTS construction_vendor_payments (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    project_id BIGINT NOT NULL REFERENCES construction_projects(id),
    vendor_id BIGINT NOT NULL REFERENCES construction_project_vendors(id),

    -- Payment milestone info
    milestone_name VARCHAR(255),
    milestone_description TEXT,

    -- Amount
    planned_amount DECIMAL(18,2) NOT NULL,
    currency VARCHAR(3) DEFAULT 'UZS',

    -- Dates
    planned_date DATE,
    actual_date DATE,

    -- Link to actual payment in Finance module
    payment_id BIGINT REFERENCES payments(id),

    -- Payment conditions
    condition_type VARCHAR(50),
    condition_details TEXT,
    linked_smeta_section_id BIGINT REFERENCES smeta_sections(id),
    completion_percent_required DECIMAL(5,2),

    -- Status
    status VARCHAR(50) DEFAULT 'pending',

    notes TEXT,
    created_date TIMESTAMP DEFAULT NOW(),
    updated_date TIMESTAMP DEFAULT NOW()
);

-- 15. Construction Site Warehouse
CREATE TABLE IF NOT EXISTS construction_site_warehouses (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    project_id BIGINT NOT NULL REFERENCES construction_projects(id),

    -- Link to main Inventory warehouse system
    warehouse_id BIGINT REFERENCES warehouses(id),

    -- Site warehouse info
    name VARCHAR(255) NOT NULL,
    location_description TEXT,
    gps_coordinates JSONB,

    -- Responsible person
    warehouse_keeper_id BIGINT REFERENCES employees(id),

    -- Capacity info
    total_area DECIMAL(10,2),
    covered_area DECIMAL(10,2),

    is_active BOOLEAN DEFAULT true,

    notes TEXT,
    created_date TIMESTAMP DEFAULT NOW(),
    updated_date TIMESTAMP DEFAULT NOW(),

    UNIQUE(project_id, warehouse_id)
);

-- =====================================================
-- INDEXES
-- =====================================================

CREATE INDEX IF NOT EXISTS idx_construction_projects_tenant ON construction_projects(tenant_id);
CREATE INDEX IF NOT EXISTS idx_construction_projects_status ON construction_projects(status);
CREATE INDEX IF NOT EXISTS idx_construction_projects_org ON construction_projects(organization_id);
CREATE INDEX IF NOT EXISTS idx_smeta_sections_project ON smeta_sections(project_id);
CREATE INDEX IF NOT EXISTS idx_smeta_items_section ON smeta_items(section_id);
CREATE INDEX IF NOT EXISTS idx_smeta_resources_item ON smeta_resources(smeta_item_id);
CREATE INDEX IF NOT EXISTS idx_photo_reports_project ON construction_photo_reports(project_id);
CREATE INDEX IF NOT EXISTS idx_daily_reports_project_date ON construction_daily_reports(project_id, report_date);
CREATE INDEX IF NOT EXISTS idx_work_progress_project ON construction_work_progress(project_id);
CREATE INDEX IF NOT EXISTS idx_material_requests_project ON construction_material_requests(project_id);
CREATE INDEX IF NOT EXISTS idx_project_vendors_project ON construction_project_vendors(project_id);
CREATE INDEX IF NOT EXISTS idx_project_vendors_vendor ON construction_project_vendors(vendor_id);
CREATE INDEX IF NOT EXISTS idx_material_deliveries_project ON construction_material_deliveries(project_id);
CREATE INDEX IF NOT EXISTS idx_material_deliveries_vendor ON construction_material_deliveries(vendor_id);
CREATE INDEX IF NOT EXISTS idx_material_issues_project ON construction_material_issues(project_id);
CREATE INDEX IF NOT EXISTS idx_material_issues_smeta ON construction_material_issues(smeta_item_id);
CREATE INDEX IF NOT EXISTS idx_vendor_payments_vendor ON construction_vendor_payments(vendor_id);
CREATE INDEX IF NOT EXISTS idx_vendor_payments_status ON construction_vendor_payments(status);
CREATE INDEX IF NOT EXISTS idx_site_warehouses_project ON construction_site_warehouses(project_id);

-- =====================================================
-- PERMISSIONS
-- =====================================================

INSERT INTO permissions (name, description, module, action) VALUES
('construction.view', 'View construction projects', 'construction', 'read'),
('construction.create', 'Create construction projects', 'construction', 'create'),
('construction.edit', 'Edit construction projects', 'construction', 'update'),
('construction.delete', 'Delete construction projects', 'construction', 'delete'),
('construction.approve_smeta', 'Approve smeta/estimates', 'construction', 'approve'),
('construction.manage_team', 'Manage project team', 'construction', 'manage'),
('construction.photo_reports', 'Submit photo reports', 'construction', 'report'),
('construction.verify_progress', 'Verify work progress', 'construction', 'verify'),
('construction.financial', 'Access financial data', 'construction', 'financial')
ON CONFLICT (name) DO NOTHING;
