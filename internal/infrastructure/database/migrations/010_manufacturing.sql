-- Manufacturing Module Enhancement Migration
-- Adds production orders, work orders, work centers, quality control, and MRP tables

-- =====================================================
-- WORK CENTERS (Manufacturing Resources)
-- =====================================================
CREATE TABLE IF NOT EXISTS work_centers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    code VARCHAR(50) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,

    -- Location & Department
    warehouse_id UUID REFERENCES warehouses(id),
    department VARCHAR(100),

    -- Capacity & Scheduling
    capacity_per_hour DECIMAL(10,2) DEFAULT 1,
    efficiency_factor DECIMAL(5,2) DEFAULT 100, -- percentage
    oee_target DECIMAL(5,2) DEFAULT 85, -- Overall Equipment Effectiveness target
    working_hours_per_day DECIMAL(4,2) DEFAULT 8,

    -- Costing
    hourly_cost DECIMAL(15,2) DEFAULT 0,
    setup_cost DECIMAL(15,2) DEFAULT 0,
    overhead_cost DECIMAL(15,2) DEFAULT 0,
    currency VARCHAR(3) DEFAULT 'USD',

    -- Status & Availability
    status VARCHAR(20) DEFAULT 'active' CHECK (status IN ('active', 'inactive', 'maintenance', 'retired')),
    is_available BOOLEAN DEFAULT true,
    next_maintenance_date DATE,
    last_maintenance_date DATE,

    -- Tracking
    total_jobs_completed INTEGER DEFAULT 0,
    total_hours_worked DECIMAL(10,2) DEFAULT 0,
    current_utilization DECIMAL(5,2) DEFAULT 0, -- percentage

    -- Metadata
    notes TEXT,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,

    UNIQUE(tenant_id, code)
);

CREATE INDEX idx_work_centers_tenant ON work_centers(tenant_id);
CREATE INDEX idx_work_centers_status ON work_centers(status) WHERE deleted_at IS NULL;
CREATE INDEX idx_work_centers_warehouse ON work_centers(warehouse_id) WHERE deleted_at IS NULL;

-- =====================================================
-- PRODUCTION ORDERS (Manufacturing Orders)
-- =====================================================
CREATE TABLE IF NOT EXISTS production_orders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    code VARCHAR(50) NOT NULL,
    name VARCHAR(255),

    -- Product Info
    product_id UUID NOT NULL REFERENCES products(id),
    bom_id UUID REFERENCES product_boms(id),
    quantity_planned DECIMAL(15,4) NOT NULL,
    quantity_produced DECIMAL(15,4) DEFAULT 0,
    quantity_scrapped DECIMAL(15,4) DEFAULT 0,
    uom VARCHAR(20) NOT NULL,

    -- Scheduling
    scheduled_start DATE,
    scheduled_end DATE,
    actual_start TIMESTAMP WITH TIME ZONE,
    actual_end TIMESTAMP WITH TIME ZONE,
    priority INTEGER DEFAULT 5 CHECK (priority BETWEEN 1 AND 10), -- 1=highest

    -- Status Tracking
    status VARCHAR(30) DEFAULT 'draft' CHECK (status IN (
        'draft', 'confirmed', 'ready', 'in_progress',
        'paused', 'completed', 'cancelled', 'closed'
    )),
    progress_percent DECIMAL(5,2) DEFAULT 0,

    -- Source & References
    source_type VARCHAR(30), -- 'manual', 'mrp', 'sales_order', 'reorder_rule'
    source_id UUID, -- Reference to source document
    sales_order_id UUID,
    customer_id UUID REFERENCES contacts(id),

    -- Location
    warehouse_id UUID REFERENCES warehouses(id),
    location_id UUID REFERENCES warehouse_locations(id),

    -- Costing
    planned_cost DECIMAL(15,2) DEFAULT 0,
    actual_cost DECIMAL(15,2) DEFAULT 0,
    material_cost DECIMAL(15,2) DEFAULT 0,
    labor_cost DECIMAL(15,2) DEFAULT 0,
    overhead_cost DECIMAL(15,2) DEFAULT 0,
    currency VARCHAR(3) DEFAULT 'USD',

    -- Assignment
    assigned_to UUID REFERENCES users(id),
    work_center_id UUID REFERENCES work_centers(id),

    -- Quality
    requires_quality_check BOOLEAN DEFAULT false,
    quality_status VARCHAR(20) CHECK (quality_status IN ('pending', 'passed', 'failed', 'partial')),

    -- Notes & Metadata
    notes TEXT,
    internal_notes TEXT,
    tags JSONB DEFAULT '[]',

    created_by UUID REFERENCES users(id),
    confirmed_by UUID REFERENCES users(id),
    confirmed_at TIMESTAMP WITH TIME ZONE,
    completed_by UUID REFERENCES users(id),
    completed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,

    UNIQUE(tenant_id, code)
);

CREATE INDEX idx_production_orders_tenant ON production_orders(tenant_id);
CREATE INDEX idx_production_orders_status ON production_orders(status) WHERE deleted_at IS NULL;
CREATE INDEX idx_production_orders_product ON production_orders(product_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_production_orders_scheduled ON production_orders(scheduled_start, scheduled_end) WHERE deleted_at IS NULL;
CREATE INDEX idx_production_orders_work_center ON production_orders(work_center_id) WHERE deleted_at IS NULL;

-- =====================================================
-- WORK ORDERS (Operations within Production Orders)
-- =====================================================
CREATE TABLE IF NOT EXISTS work_orders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    production_order_id UUID NOT NULL REFERENCES production_orders(id) ON DELETE CASCADE,
    code VARCHAR(50) NOT NULL,
    name VARCHAR(255),

    -- Operation Details
    sequence INTEGER DEFAULT 1,
    operation_id UUID REFERENCES bom_operations(id),
    work_center_id UUID REFERENCES work_centers(id),

    -- Quantity
    quantity_to_produce DECIMAL(15,4) NOT NULL,
    quantity_produced DECIMAL(15,4) DEFAULT 0,
    quantity_scrapped DECIMAL(15,4) DEFAULT 0,
    uom VARCHAR(20) NOT NULL,

    -- Time Tracking
    planned_duration_hours DECIMAL(8,2) DEFAULT 0,
    actual_duration_hours DECIMAL(8,2) DEFAULT 0,
    setup_time_hours DECIMAL(8,2) DEFAULT 0,

    scheduled_start TIMESTAMP WITH TIME ZONE,
    scheduled_end TIMESTAMP WITH TIME ZONE,
    actual_start TIMESTAMP WITH TIME ZONE,
    actual_end TIMESTAMP WITH TIME ZONE,

    -- Status
    status VARCHAR(30) DEFAULT 'pending' CHECK (status IN (
        'pending', 'ready', 'in_progress', 'paused',
        'waiting_material', 'waiting_qc', 'completed', 'cancelled'
    )),
    progress_percent DECIMAL(5,2) DEFAULT 0,

    -- Costing
    planned_cost DECIMAL(15,2) DEFAULT 0,
    actual_cost DECIMAL(15,2) DEFAULT 0,
    labor_cost DECIMAL(15,2) DEFAULT 0,
    machine_cost DECIMAL(15,2) DEFAULT 0,

    -- Assignment
    assigned_to UUID REFERENCES users(id),

    -- Instructions
    instructions TEXT,

    -- Notes
    notes TEXT,

    created_by UUID REFERENCES users(id),
    started_by UUID REFERENCES users(id),
    completed_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,

    UNIQUE(tenant_id, code)
);

CREATE INDEX idx_work_orders_tenant ON work_orders(tenant_id);
CREATE INDEX idx_work_orders_production ON work_orders(production_order_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_work_orders_status ON work_orders(status) WHERE deleted_at IS NULL;
CREATE INDEX idx_work_orders_work_center ON work_orders(work_center_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_work_orders_scheduled ON work_orders(scheduled_start, scheduled_end) WHERE deleted_at IS NULL;

-- =====================================================
-- WORK ORDER TIME LOGS (Time Tracking)
-- =====================================================
CREATE TABLE IF NOT EXISTS work_order_time_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    work_order_id UUID NOT NULL REFERENCES work_orders(id) ON DELETE CASCADE,

    -- Time Entry
    start_time TIMESTAMP WITH TIME ZONE NOT NULL,
    end_time TIMESTAMP WITH TIME ZONE,
    duration_hours DECIMAL(8,2),

    -- Type
    log_type VARCHAR(20) DEFAULT 'work' CHECK (log_type IN ('setup', 'work', 'pause', 'maintenance', 'other')),

    -- Worker
    worker_id UUID REFERENCES users(id),
    worker_name VARCHAR(255),

    -- Notes
    notes TEXT,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_work_order_time_logs_work_order ON work_order_time_logs(work_order_id);
CREATE INDEX idx_work_order_time_logs_worker ON work_order_time_logs(worker_id);

-- =====================================================
-- MATERIAL CONSUMPTION (Production Material Usage)
-- =====================================================
CREATE TABLE IF NOT EXISTS production_material_consumption (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    production_order_id UUID NOT NULL REFERENCES production_orders(id) ON DELETE CASCADE,
    work_order_id UUID REFERENCES work_orders(id),

    -- Material
    product_id UUID NOT NULL REFERENCES products(id),
    bom_line_id UUID REFERENCES bom_lines(id),

    -- Quantity
    quantity_planned DECIMAL(15,4) NOT NULL,
    quantity_consumed DECIMAL(15,4) DEFAULT 0,
    quantity_returned DECIMAL(15,4) DEFAULT 0,
    uom VARCHAR(20) NOT NULL,

    -- Lot/Serial Tracking
    lot_number VARCHAR(100),
    serial_number VARCHAR(100),

    -- Location
    warehouse_id UUID REFERENCES warehouses(id),
    location_id UUID REFERENCES warehouse_locations(id),

    -- Costing
    unit_cost DECIMAL(15,4) DEFAULT 0,
    total_cost DECIMAL(15,2) DEFAULT 0,

    -- Status
    status VARCHAR(20) DEFAULT 'planned' CHECK (status IN ('planned', 'reserved', 'consumed', 'returned')),

    -- Tracking
    consumed_at TIMESTAMP WITH TIME ZONE,
    consumed_by UUID REFERENCES users(id),
    inventory_transaction_id UUID,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_material_consumption_production ON production_material_consumption(production_order_id);
CREATE INDEX idx_material_consumption_product ON production_material_consumption(product_id);

-- =====================================================
-- QUALITY CONTROL POINTS
-- =====================================================
CREATE TABLE IF NOT EXISTS quality_control_points (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    code VARCHAR(50) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,

    -- Trigger
    trigger_type VARCHAR(30) DEFAULT 'operation' CHECK (trigger_type IN (
        'operation', 'production_start', 'production_end',
        'receiving', 'shipping', 'periodic'
    )),

    -- Linked Resources
    product_id UUID REFERENCES products(id),
    bom_id UUID REFERENCES product_boms(id),
    operation_id UUID REFERENCES bom_operations(id),
    work_center_id UUID REFERENCES work_centers(id),

    -- Inspection Details
    inspection_type VARCHAR(30) DEFAULT 'manual' CHECK (inspection_type IN (
        'manual', 'automated', 'sampling', 'destructive'
    )),
    sampling_percentage DECIMAL(5,2) DEFAULT 100,

    -- Pass/Fail Criteria
    measurement_type VARCHAR(30), -- 'boolean', 'numeric', 'text', 'checklist'
    target_value DECIMAL(15,4),
    min_value DECIMAL(15,4),
    max_value DECIMAL(15,4),
    tolerance DECIMAL(15,4),

    -- Actions
    failure_action VARCHAR(30) DEFAULT 'hold' CHECK (failure_action IN (
        'hold', 'scrap', 'rework', 'notify', 'continue'
    )),

    is_active BOOLEAN DEFAULT true,
    is_mandatory BOOLEAN DEFAULT false,

    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,

    UNIQUE(tenant_id, code)
);

CREATE INDEX idx_qc_points_tenant ON quality_control_points(tenant_id);
CREATE INDEX idx_qc_points_product ON quality_control_points(product_id) WHERE deleted_at IS NULL;

-- =====================================================
-- QUALITY CHECKS (Inspection Results)
-- =====================================================
CREATE TABLE IF NOT EXISTS quality_checks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    code VARCHAR(50) NOT NULL,

    -- References
    quality_control_point_id UUID REFERENCES quality_control_points(id),
    production_order_id UUID REFERENCES production_orders(id),
    work_order_id UUID REFERENCES work_orders(id),
    product_id UUID REFERENCES products(id),
    lot_number VARCHAR(100),

    -- Inspection Details
    inspection_date TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    inspector_id UUID REFERENCES users(id),
    inspector_name VARCHAR(255),

    -- Quantity
    quantity_inspected DECIMAL(15,4) NOT NULL,
    quantity_passed DECIMAL(15,4) DEFAULT 0,
    quantity_failed DECIMAL(15,4) DEFAULT 0,

    -- Results
    result VARCHAR(20) DEFAULT 'pending' CHECK (result IN ('pending', 'passed', 'failed', 'partial')),
    measured_value DECIMAL(15,4),
    measurement_unit VARCHAR(20),

    -- Pass/Fail Details
    pass_rate DECIMAL(5,2), -- percentage
    defect_type VARCHAR(100),
    defect_category VARCHAR(50),

    -- Actions Taken
    action_taken VARCHAR(30) CHECK (action_taken IN ('none', 'rework', 'scrap', 'hold', 'released')),
    rework_order_id UUID,
    scrap_order_id UUID REFERENCES scrap_orders(id),

    -- Notes
    notes TEXT,
    failure_reason TEXT,
    corrective_action TEXT,

    -- Attachments (photos, documents)
    attachments JSONB DEFAULT '[]',

    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,

    UNIQUE(tenant_id, code)
);

CREATE INDEX idx_quality_checks_tenant ON quality_checks(tenant_id);
CREATE INDEX idx_quality_checks_production ON quality_checks(production_order_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_quality_checks_result ON quality_checks(result) WHERE deleted_at IS NULL;
CREATE INDEX idx_quality_checks_date ON quality_checks(inspection_date) WHERE deleted_at IS NULL;

-- =====================================================
-- QUALITY DEFECTS (Defect Registry)
-- =====================================================
CREATE TABLE IF NOT EXISTS quality_defects (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    code VARCHAR(50) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,

    category VARCHAR(50), -- 'dimensional', 'visual', 'functional', 'material', 'other'
    severity VARCHAR(20) DEFAULT 'minor' CHECK (severity IN ('critical', 'major', 'minor', 'cosmetic')),

    -- Default Action
    default_action VARCHAR(30) DEFAULT 'rework' CHECK (default_action IN ('scrap', 'rework', 'use_as_is', 'return')),

    is_active BOOLEAN DEFAULT true,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,

    UNIQUE(tenant_id, code)
);

CREATE INDEX idx_quality_defects_tenant ON quality_defects(tenant_id);

-- =====================================================
-- MRP DEMAND (Material Requirements Planning)
-- =====================================================
CREATE TABLE IF NOT EXISTS mrp_demand (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),

    -- Product
    product_id UUID NOT NULL REFERENCES products(id),

    -- Demand Source
    source_type VARCHAR(30) NOT NULL CHECK (source_type IN (
        'sales_order', 'forecast', 'production_order',
        'safety_stock', 'manual', 'transfer'
    )),
    source_id UUID,
    source_reference VARCHAR(100),

    -- Quantity
    quantity_required DECIMAL(15,4) NOT NULL,
    quantity_reserved DECIMAL(15,4) DEFAULT 0,
    quantity_fulfilled DECIMAL(15,4) DEFAULT 0,
    uom VARCHAR(20) NOT NULL,

    -- Dates
    required_date DATE NOT NULL,
    priority INTEGER DEFAULT 5,

    -- Location
    warehouse_id UUID REFERENCES warehouses(id),

    -- Status
    status VARCHAR(20) DEFAULT 'open' CHECK (status IN ('open', 'partially_fulfilled', 'fulfilled', 'cancelled')),

    -- Supply Link (if fulfilled by specific supply)
    supply_type VARCHAR(30), -- 'purchase_order', 'production_order', 'transfer', 'stock'
    supply_id UUID,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_mrp_demand_tenant ON mrp_demand(tenant_id);
CREATE INDEX idx_mrp_demand_product ON mrp_demand(product_id);
CREATE INDEX idx_mrp_demand_date ON mrp_demand(required_date);
CREATE INDEX idx_mrp_demand_status ON mrp_demand(status);

-- =====================================================
-- MRP SUPPLY (Available/Incoming Supply)
-- =====================================================
CREATE TABLE IF NOT EXISTS mrp_supply (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),

    -- Product
    product_id UUID NOT NULL REFERENCES products(id),

    -- Supply Source
    source_type VARCHAR(30) NOT NULL CHECK (source_type IN (
        'on_hand', 'purchase_order', 'production_order',
        'transfer_in', 'return'
    )),
    source_id UUID,
    source_reference VARCHAR(100),

    -- Quantity
    quantity_available DECIMAL(15,4) NOT NULL,
    quantity_reserved DECIMAL(15,4) DEFAULT 0,
    uom VARCHAR(20) NOT NULL,

    -- Dates
    available_date DATE NOT NULL,

    -- Location
    warehouse_id UUID REFERENCES warehouses(id),

    -- Status
    status VARCHAR(20) DEFAULT 'expected' CHECK (status IN ('on_hand', 'expected', 'received', 'cancelled')),

    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_mrp_supply_tenant ON mrp_supply(tenant_id);
CREATE INDEX idx_mrp_supply_product ON mrp_supply(product_id);
CREATE INDEX idx_mrp_supply_date ON mrp_supply(available_date);

-- =====================================================
-- MRP RECOMMENDATIONS (System Generated Suggestions)
-- =====================================================
CREATE TABLE IF NOT EXISTS mrp_recommendations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),

    -- Product
    product_id UUID NOT NULL REFERENCES products(id),

    -- Recommendation
    recommendation_type VARCHAR(30) NOT NULL CHECK (recommendation_type IN (
        'purchase', 'manufacture', 'transfer', 'cancel', 'reschedule'
    )),

    quantity DECIMAL(15,4) NOT NULL,
    uom VARCHAR(20) NOT NULL,

    -- Dates
    recommended_date DATE NOT NULL,
    due_date DATE,

    -- Source Demand
    demand_id UUID REFERENCES mrp_demand(id),

    -- Priority & Urgency
    priority INTEGER DEFAULT 5,
    urgency VARCHAR(20) DEFAULT 'normal' CHECK (urgency IN ('critical', 'high', 'normal', 'low')),

    -- Status
    status VARCHAR(20) DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'rejected', 'executed')),

    -- Execution Reference (if accepted)
    executed_type VARCHAR(30), -- 'purchase_order', 'production_order'
    executed_id UUID,
    executed_at TIMESTAMP WITH TIME ZONE,
    executed_by UUID REFERENCES users(id),

    -- Reason & Notes
    reason TEXT,
    notes TEXT,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_mrp_recommendations_tenant ON mrp_recommendations(tenant_id);
CREATE INDEX idx_mrp_recommendations_product ON mrp_recommendations(product_id);
CREATE INDEX idx_mrp_recommendations_status ON mrp_recommendations(status);

-- =====================================================
-- MANUFACTURING SHIFTS
-- =====================================================
CREATE TABLE IF NOT EXISTS manufacturing_shifts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    code VARCHAR(50) NOT NULL,
    name VARCHAR(100) NOT NULL,

    -- Time
    start_time TIME NOT NULL,
    end_time TIME NOT NULL,
    break_duration_minutes INTEGER DEFAULT 0,

    -- Days Active (bitmask: 1=Mon, 2=Tue, 4=Wed, 8=Thu, 16=Fri, 32=Sat, 64=Sun)
    active_days INTEGER DEFAULT 31, -- Mon-Fri by default

    is_active BOOLEAN DEFAULT true,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,

    UNIQUE(tenant_id, code)
);

-- =====================================================
-- WORK CENTER CALENDAR (Availability)
-- =====================================================
CREATE TABLE IF NOT EXISTS work_center_calendar (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    work_center_id UUID NOT NULL REFERENCES work_centers(id) ON DELETE CASCADE,

    -- Date Range
    date DATE NOT NULL,
    shift_id UUID REFERENCES manufacturing_shifts(id),

    -- Availability
    is_available BOOLEAN DEFAULT true,
    available_hours DECIMAL(4,2),

    -- Reason for unavailability
    unavailable_reason VARCHAR(50), -- 'maintenance', 'holiday', 'breakdown', 'no_operator'

    notes TEXT,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),

    UNIQUE(work_center_id, date, shift_id)
);

CREATE INDEX idx_wc_calendar_work_center ON work_center_calendar(work_center_id);
CREATE INDEX idx_wc_calendar_date ON work_center_calendar(date);

-- =====================================================
-- EQUIPMENT/MACHINE REGISTRY
-- =====================================================
CREATE TABLE IF NOT EXISTS manufacturing_equipment (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    code VARCHAR(50) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,

    -- Classification
    equipment_type VARCHAR(50), -- 'machine', 'tool', 'fixture', 'gauge'
    category VARCHAR(100),

    -- Location
    work_center_id UUID REFERENCES work_centers(id),
    warehouse_id UUID REFERENCES warehouses(id),

    -- Details
    manufacturer VARCHAR(255),
    model VARCHAR(255),
    serial_number VARCHAR(100),
    purchase_date DATE,
    warranty_expiry DATE,

    -- Status
    status VARCHAR(20) DEFAULT 'operational' CHECK (status IN (
        'operational', 'maintenance', 'repair', 'retired', 'disposed'
    )),

    -- Maintenance
    last_maintenance_date DATE,
    next_maintenance_date DATE,
    maintenance_interval_days INTEGER,

    -- Costing
    purchase_cost DECIMAL(15,2),
    current_value DECIMAL(15,2),
    hourly_rate DECIMAL(10,2),

    notes TEXT,

    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,

    UNIQUE(tenant_id, code)
);

CREATE INDEX idx_equipment_tenant ON manufacturing_equipment(tenant_id);
CREATE INDEX idx_equipment_work_center ON manufacturing_equipment(work_center_id) WHERE deleted_at IS NULL;

-- =====================================================
-- MAINTENANCE RECORDS
-- =====================================================
CREATE TABLE IF NOT EXISTS equipment_maintenance (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    equipment_id UUID NOT NULL REFERENCES manufacturing_equipment(id),
    work_center_id UUID REFERENCES work_centers(id),

    -- Maintenance Type
    maintenance_type VARCHAR(30) DEFAULT 'preventive' CHECK (maintenance_type IN (
        'preventive', 'corrective', 'predictive', 'breakdown'
    )),

    -- Schedule
    scheduled_date DATE,
    actual_date DATE,
    duration_hours DECIMAL(6,2),

    -- Details
    description TEXT,
    work_performed TEXT,
    parts_used JSONB DEFAULT '[]',

    -- Status
    status VARCHAR(20) DEFAULT 'scheduled' CHECK (status IN (
        'scheduled', 'in_progress', 'completed', 'cancelled'
    )),

    -- Costing
    labor_cost DECIMAL(15,2) DEFAULT 0,
    parts_cost DECIMAL(15,2) DEFAULT 0,
    total_cost DECIMAL(15,2) DEFAULT 0,

    -- Personnel
    performed_by UUID REFERENCES users(id),
    approved_by UUID REFERENCES users(id),

    notes TEXT,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_maintenance_equipment ON equipment_maintenance(equipment_id);
CREATE INDEX idx_maintenance_status ON equipment_maintenance(status);
CREATE INDEX idx_maintenance_date ON equipment_maintenance(scheduled_date);

-- =====================================================
-- DOWNTIME LOGS
-- =====================================================
CREATE TABLE IF NOT EXISTS downtime_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),

    -- Resource
    work_center_id UUID REFERENCES work_centers(id),
    equipment_id UUID REFERENCES manufacturing_equipment(id),
    production_order_id UUID REFERENCES production_orders(id),
    work_order_id UUID REFERENCES work_orders(id),

    -- Time
    start_time TIMESTAMP WITH TIME ZONE NOT NULL,
    end_time TIMESTAMP WITH TIME ZONE,
    duration_minutes INTEGER,

    -- Classification
    downtime_type VARCHAR(30) DEFAULT 'unplanned' CHECK (downtime_type IN (
        'planned', 'unplanned', 'changeover', 'maintenance', 'no_material', 'no_operator'
    )),

    reason VARCHAR(255),
    category VARCHAR(50), -- 'mechanical', 'electrical', 'quality', 'material', 'operator', 'other'

    -- Impact
    units_lost DECIMAL(15,4),
    cost_impact DECIMAL(15,2),

    -- Resolution
    resolution TEXT,
    resolved_by UUID REFERENCES users(id),

    notes TEXT,

    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_downtime_work_center ON downtime_logs(work_center_id);
CREATE INDEX idx_downtime_equipment ON downtime_logs(equipment_id);
CREATE INDEX idx_downtime_time ON downtime_logs(start_time, end_time);

-- =====================================================
-- OEE METRICS (Overall Equipment Effectiveness)
-- =====================================================
CREATE TABLE IF NOT EXISTS oee_metrics (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    work_center_id UUID NOT NULL REFERENCES work_centers(id),

    -- Period
    metric_date DATE NOT NULL,
    shift_id UUID REFERENCES manufacturing_shifts(id),

    -- Time Metrics (minutes)
    planned_production_time INTEGER DEFAULT 0,
    actual_run_time INTEGER DEFAULT 0,
    downtime_minutes INTEGER DEFAULT 0,

    -- Production Metrics
    total_units_produced DECIMAL(15,4) DEFAULT 0,
    good_units DECIMAL(15,4) DEFAULT 0,
    defective_units DECIMAL(15,4) DEFAULT 0,
    ideal_cycle_time DECIMAL(10,4), -- seconds per unit

    -- OEE Components (percentages)
    availability DECIMAL(5,2) DEFAULT 0,  -- Run Time / Planned Time
    performance DECIMAL(5,2) DEFAULT 0,   -- (Ideal Cycle × Units) / Run Time
    quality DECIMAL(5,2) DEFAULT 0,       -- Good Units / Total Units
    oee DECIMAL(5,2) DEFAULT 0,           -- Availability × Performance × Quality

    -- Targets
    availability_target DECIMAL(5,2),
    performance_target DECIMAL(5,2),
    quality_target DECIMAL(5,2),
    oee_target DECIMAL(5,2),

    notes TEXT,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),

    UNIQUE(work_center_id, metric_date, shift_id)
);

CREATE INDEX idx_oee_work_center ON oee_metrics(work_center_id);
CREATE INDEX idx_oee_date ON oee_metrics(metric_date);

-- =====================================================
-- Add permissions for manufacturing module
-- =====================================================
INSERT INTO permissions (id, tenant_id, code, name, description, module, action, resource)
SELECT gen_random_uuid(), t.id,
    'manufacturing.' || r.resource || '.' || a.action,
    INITCAP(a.action) || ' ' || INITCAP(REPLACE(r.resource, '_', ' ')),
    'Permission to ' || a.action || ' ' || REPLACE(r.resource, '_', ' '),
    'manufacturing', a.action, r.resource
FROM tenants t
CROSS JOIN (VALUES
    ('production_orders'), ('work_orders'), ('work_centers'),
    ('quality_checks'), ('equipment'), ('mrp')
) AS r(resource)
CROSS JOIN (VALUES ('read'), ('create'), ('update'), ('delete')) AS a(action)
WHERE NOT EXISTS (
    SELECT 1 FROM permissions p
    WHERE p.tenant_id = t.id
    AND p.code = 'manufacturing.' || r.resource || '.' || a.action
);

-- Add approval permissions
INSERT INTO permissions (id, tenant_id, code, name, description, module, action, resource)
SELECT gen_random_uuid(), t.id,
    'manufacturing.' || r.resource || '.approve',
    'Approve ' || INITCAP(REPLACE(r.resource, '_', ' ')),
    'Permission to approve ' || REPLACE(r.resource, '_', ' '),
    'manufacturing', 'approve', r.resource
FROM tenants t
CROSS JOIN (VALUES ('production_orders'), ('quality_checks')) AS r(resource)
WHERE NOT EXISTS (
    SELECT 1 FROM permissions p
    WHERE p.tenant_id = t.id
    AND p.code = 'manufacturing.' || r.resource || '.approve'
);
