-- Migration 142: Stock Operations TT (Ombor Operatsiyalari)
-- Multi-step configurable warehouse operations: Receipt, Delivery, Internal Transfer, Write-off
-- Based on Genix_ERP_Ombor_Operatsiyalari_TT.docx spec

-- ─── 1. Extend warehouse_operation_types with TT fields ───────────────────────

ALTER TABLE warehouse_operation_types
    ADD COLUMN IF NOT EXISTS direction VARCHAR(50)
        CHECK (direction IN ('receipt','delivery','internal','write_off')),
    ADD COLUMN IF NOT EXISTS sequence_prefix VARCHAR(20),         -- e.g. REC-, DEL-, INT-, WO-
    ADD COLUMN IF NOT EXISTS auto_post_accounting BOOLEAN DEFAULT true,
    ADD COLUMN IF NOT EXISTS barcode_mode VARCHAR(20) DEFAULT 'single'
        CHECK (barcode_mode IN ('single','batch')),
    ADD COLUMN IF NOT EXISTS auto_print BOOLEAN DEFAULT false,
    ADD COLUMN IF NOT EXISTS journal_id UUID REFERENCES journals(id);

-- Back-fill direction from operation_type for existing rows
UPDATE warehouse_operation_types
SET direction = CASE operation_type
    WHEN 'incoming' THEN 'receipt'
    WHEN 'outgoing' THEN 'delivery'
    ELSE 'internal'
END
WHERE direction IS NULL;

-- Back-fill sequence_prefix
UPDATE warehouse_operation_types
SET sequence_prefix = CASE direction
    WHEN 'receipt'    THEN 'REC-'
    WHEN 'delivery'   THEN 'DEL-'
    WHEN 'write_off'  THEN 'WO-'
    ELSE 'INT-'
END
WHERE sequence_prefix IS NULL;

-- ─── 2. Operation type steps (multi-step configuration) ───────────────────────

CREATE TABLE IF NOT EXISTS operation_type_steps (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    operation_type_id UUID NOT NULL REFERENCES warehouse_operation_types(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL DEFAULT 1,
    name VARCHAR(255) NOT NULL,
    source_location_id UUID REFERENCES warehouse_locations(id),
    dest_location_id UUID REFERENCES warehouse_locations(id),
    responsible_role VARCHAR(100),
    requires_approval BOOLEAN DEFAULT false,
    approval_role VARCHAR(100),
    auto_proceed BOOLEAN DEFAULT false,          -- auto-start when previous step completes
    max_duration_hours FLOAT,
    on_timeout VARCHAR(20) DEFAULT 'warn' CHECK (on_timeout IN ('warn','escalate','block')),
    notify_users JSONB DEFAULT '[]',             -- array of user IDs
    required_documents JSONB DEFAULT '[]',       -- array of document types required
    instructions TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_op_type_steps_op_type ON operation_type_steps(operation_type_id);
CREATE INDEX IF NOT EXISTS idx_op_type_steps_tenant ON operation_type_steps(tenant_id);
CREATE INDEX IF NOT EXISTS idx_op_type_steps_seq ON operation_type_steps(operation_type_id, sequence);

-- ─── 3. Stock operations (the actual operations / pickings) ───────────────────

CREATE TABLE IF NOT EXISTS stock_operations (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id),
    name VARCHAR(100) NOT NULL,                  -- REC-2026-00001
    operation_type_id UUID NOT NULL REFERENCES warehouse_operation_types(id),
    direction VARCHAR(50) NOT NULL
        CHECK (direction IN ('receipt','delivery','internal','write_off')),
    date TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    scheduled_date TIMESTAMP WITH TIME ZONE,
    partner_id UUID REFERENCES contacts(id),     -- vendor or customer
    source_document VARCHAR(255),                -- PO number, SO number, etc.
    source_location_id UUID REFERENCES warehouse_locations(id),
    dest_location_id UUID REFERENCES warehouse_locations(id),
    state VARCHAR(50) DEFAULT 'draft'
        CHECK (state IN ('draft','in_progress','waiting','done','cancelled')),
    current_step INTEGER DEFAULT 1,
    total_steps INTEGER DEFAULT 1,
    priority VARCHAR(20) DEFAULT 'normal'
        CHECK (priority IN ('low','normal','high','urgent')),
    backorder_id UUID REFERENCES stock_operations(id),
    responsible_id UUID REFERENCES users(id),
    note TEXT,
    -- Write-off specific
    write_off_reason VARCHAR(100)
        CHECK (write_off_reason IN ('damage','expired','lost','sample','donation','inventory_diff','other')),
    -- Accounting
    accounting_posted BOOLEAN DEFAULT false,
    journal_entry_id UUID,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    done_at TIMESTAMP WITH TIME ZONE,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_stock_ops_tenant ON stock_operations(tenant_id);
CREATE INDEX IF NOT EXISTS idx_stock_ops_op_type ON stock_operations(operation_type_id);
CREATE INDEX IF NOT EXISTS idx_stock_ops_state ON stock_operations(state);
CREATE INDEX IF NOT EXISTS idx_stock_ops_direction ON stock_operations(direction);
CREATE INDEX IF NOT EXISTS idx_stock_ops_partner ON stock_operations(partner_id);
CREATE INDEX IF NOT EXISTS idx_stock_ops_date ON stock_operations(date);

-- ─── 4. Stock operation lines (individual product rows) ───────────────────────

CREATE TABLE IF NOT EXISTS stock_operation_lines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    operation_id UUID NOT NULL REFERENCES stock_operations(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES products(id),
    expected_qty DECIMAL(20,4) DEFAULT 0,        -- from PO/SO
    done_qty DECIMAL(20,4) DEFAULT 0,            -- actually processed
    uom VARCHAR(50) DEFAULT 'unit',
    unit_price DECIMAL(20,4),
    lot_id UUID REFERENCES inventory_lots(id),
    lot_number VARCHAR(255),                     -- allow manual entry too
    expiry_date DATE,
    quality_status VARCHAR(50) DEFAULT 'good'
        CHECK (quality_status IN ('good','defective','rejected')),
    source_location_id UUID REFERENCES warehouse_locations(id),
    dest_location_id UUID REFERENCES warehouse_locations(id),
    write_off_reason VARCHAR(100),               -- per-line reason (write-off ops)
    note TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_stock_op_lines_op ON stock_operation_lines(operation_id);
CREATE INDEX IF NOT EXISTS idx_stock_op_lines_product ON stock_operation_lines(product_id);
CREATE INDEX IF NOT EXISTS idx_stock_op_lines_tenant ON stock_operation_lines(tenant_id);

-- ─── 5. Step execution log (audit / per-step progress) ────────────────────────

CREATE TABLE IF NOT EXISTS stock_operation_step_log (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    operation_id UUID NOT NULL REFERENCES stock_operations(id) ON DELETE CASCADE,
    step_id UUID REFERENCES operation_type_steps(id),
    step_sequence INTEGER NOT NULL,
    step_name VARCHAR(255),
    state VARCHAR(50) DEFAULT 'pending'
        CHECK (state IN ('pending','ready','in_progress','awaiting_approval','completed','rejected')),
    started_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE,
    started_by UUID REFERENCES users(id),
    completed_by UUID REFERENCES users(id),
    approved_by UUID REFERENCES users(id),
    rejection_reason TEXT,
    documents JSONB DEFAULT '[]',
    note TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_step_log_operation ON stock_operation_step_log(operation_id);
CREATE INDEX IF NOT EXISTS idx_step_log_tenant ON stock_operation_step_log(tenant_id);
CREATE INDEX IF NOT EXISTS idx_step_log_state ON stock_operation_step_log(state);

-- ─── 6. Sequence counter for auto-naming ──────────────────────────────────────

CREATE TABLE IF NOT EXISTS stock_operation_sequences (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    direction VARCHAR(50) NOT NULL,
    year INTEGER NOT NULL,
    last_number INTEGER DEFAULT 0,
    UNIQUE(tenant_id, direction, year)
);
