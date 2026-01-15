-- Migration: 005_procurement_advanced.sql
-- Description: Add Purchase Requisitions, Goods Receipts, and Purchase Returns tables

-- Purchase Requisitions
CREATE TABLE IF NOT EXISTS purchase_requisitions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    pr_number VARCHAR(50) NOT NULL,
    requested_by VARCHAR(255) NOT NULL,
    requested_by_id UUID,
    department VARCHAR(100),
    request_date DATE NOT NULL DEFAULT CURRENT_DATE,
    required_date DATE,
    status VARCHAR(30) DEFAULT 'draft',
    priority VARCHAR(20) DEFAULT 'medium',
    purpose TEXT,
    notes TEXT,
    total_amount DECIMAL(15,2) DEFAULT 0,
    approved_by VARCHAR(255),
    approved_by_id UUID,
    approved_at TIMESTAMP WITH TIME ZONE,
    rejection_reason TEXT,
    converted_to_po UUID,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_pr_tenant ON purchase_requisitions(tenant_id);
CREATE INDEX IF NOT EXISTS idx_pr_status ON purchase_requisitions(status);
CREATE INDEX IF NOT EXISTS idx_pr_department ON purchase_requisitions(department);
CREATE INDEX IF NOT EXISTS idx_pr_deleted ON purchase_requisitions(deleted_at);

-- Purchase Requisition Lines
CREATE TABLE IF NOT EXISTS purchase_requisition_lines (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    requisition_id UUID NOT NULL REFERENCES purchase_requisitions(id) ON DELETE CASCADE,
    product_id UUID,
    product_name VARCHAR(255) NOT NULL,
    product_code VARCHAR(100),
    description TEXT,
    quantity DECIMAL(15,3) NOT NULL,
    unit VARCHAR(20) DEFAULT 'pcs',
    estimated_price DECIMAL(15,2),
    total_price DECIMAL(15,2),
    preferred_vendor UUID,
    vendor_name VARCHAR(255),
    specifications TEXT,
    approved_quantity DECIMAL(15,3),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_pr_lines_requisition ON purchase_requisition_lines(requisition_id);

-- Goods Receipts
CREATE TABLE IF NOT EXISTS goods_receipts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    gr_number VARCHAR(50) NOT NULL,
    purchase_order_id UUID NOT NULL,
    po_number VARCHAR(50),
    supplier_id UUID NOT NULL,
    supplier_name VARCHAR(255),
    receipt_date DATE NOT NULL DEFAULT CURRENT_DATE,
    received_by VARCHAR(255) NOT NULL,
    received_by_id UUID,
    warehouse_id UUID,
    warehouse_name VARCHAR(255),
    status VARCHAR(30) DEFAULT 'draft',
    quality_status VARCHAR(30) DEFAULT 'pending',
    delivery_note VARCHAR(100),
    vehicle_number VARCHAR(50),
    driver_name VARCHAR(255),
    notes TEXT,
    inspection_notes TEXT,
    inspected_by VARCHAR(255),
    inspected_at TIMESTAMP WITH TIME ZONE,
    total_quantity DECIMAL(15,3) DEFAULT 0,
    accepted_quantity DECIMAL(15,3) DEFAULT 0,
    rejected_quantity DECIMAL(15,3) DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_gr_tenant ON goods_receipts(tenant_id);
CREATE INDEX IF NOT EXISTS idx_gr_po ON goods_receipts(purchase_order_id);
CREATE INDEX IF NOT EXISTS idx_gr_supplier ON goods_receipts(supplier_id);
CREATE INDEX IF NOT EXISTS idx_gr_status ON goods_receipts(status);
CREATE INDEX IF NOT EXISTS idx_gr_deleted ON goods_receipts(deleted_at);

-- Goods Receipt Lines
CREATE TABLE IF NOT EXISTS goods_receipt_lines (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    goods_receipt_id UUID NOT NULL REFERENCES goods_receipts(id) ON DELETE CASCADE,
    po_line_id UUID,
    product_id UUID,
    product_name VARCHAR(255) NOT NULL,
    product_code VARCHAR(100),
    ordered_quantity DECIMAL(15,3) NOT NULL,
    received_quantity DECIMAL(15,3) NOT NULL,
    accepted_quantity DECIMAL(15,3) DEFAULT 0,
    rejected_quantity DECIMAL(15,3) DEFAULT 0,
    unit VARCHAR(20) DEFAULT 'pcs',
    unit_price DECIMAL(15,2),
    quality_status VARCHAR(30) DEFAULT 'pending',
    rejection_reason TEXT,
    batch_number VARCHAR(100),
    expiry_date DATE,
    serial_numbers TEXT,
    storage_location VARCHAR(100),
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_gr_lines_receipt ON goods_receipt_lines(goods_receipt_id);

-- Purchase Returns
CREATE TABLE IF NOT EXISTS purchase_returns (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    return_number VARCHAR(50) NOT NULL,
    purchase_order_id UUID,
    po_number VARCHAR(50),
    goods_receipt_id UUID,
    gr_number VARCHAR(50),
    supplier_id UUID NOT NULL,
    supplier_name VARCHAR(255),
    return_date DATE NOT NULL DEFAULT CURRENT_DATE,
    requested_by VARCHAR(255) NOT NULL,
    requested_by_id UUID,
    status VARCHAR(30) DEFAULT 'draft',
    return_reason VARCHAR(50) NOT NULL,
    reason_description TEXT,
    total_quantity DECIMAL(15,3) DEFAULT 0,
    total_value DECIMAL(15,2) DEFAULT 0,
    shipping_method VARCHAR(100),
    tracking_number VARCHAR(100),
    shipped_date DATE,
    received_date DATE,
    credit_note_number VARCHAR(50),
    credit_note_date DATE,
    credit_amount DECIMAL(15,2) DEFAULT 0,
    approved_by VARCHAR(255),
    approved_by_id UUID,
    approved_at TIMESTAMP WITH TIME ZONE,
    rejection_reason TEXT,
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_return_tenant ON purchase_returns(tenant_id);
CREATE INDEX IF NOT EXISTS idx_return_po ON purchase_returns(purchase_order_id);
CREATE INDEX IF NOT EXISTS idx_return_gr ON purchase_returns(goods_receipt_id);
CREATE INDEX IF NOT EXISTS idx_return_supplier ON purchase_returns(supplier_id);
CREATE INDEX IF NOT EXISTS idx_return_status ON purchase_returns(status);
CREATE INDEX IF NOT EXISTS idx_return_deleted ON purchase_returns(deleted_at);

-- Purchase Return Lines
CREATE TABLE IF NOT EXISTS purchase_return_lines (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    return_id UUID NOT NULL REFERENCES purchase_returns(id) ON DELETE CASCADE,
    po_line_id UUID,
    gr_line_id UUID,
    product_id UUID,
    product_name VARCHAR(255) NOT NULL,
    product_code VARCHAR(100),
    return_quantity DECIMAL(15,3) NOT NULL,
    unit VARCHAR(20) DEFAULT 'pcs',
    unit_price DECIMAL(15,2),
    total_price DECIMAL(15,2),
    return_reason VARCHAR(50),
    reason_details TEXT,
    batch_number VARCHAR(100),
    serial_numbers TEXT,
    condition VARCHAR(50),
    photos TEXT,
    received_back BOOLEAN DEFAULT FALSE,
    credit_applied BOOLEAN DEFAULT FALSE,
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_return_lines_return ON purchase_return_lines(return_id);

-- Add triggers for updated_at
CREATE OR REPLACE FUNCTION update_procurement_advanced_timestamp()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS tr_pr_updated ON purchase_requisitions;
CREATE TRIGGER tr_pr_updated BEFORE UPDATE ON purchase_requisitions
FOR EACH ROW EXECUTE FUNCTION update_procurement_advanced_timestamp();

DROP TRIGGER IF EXISTS tr_gr_updated ON goods_receipts;
CREATE TRIGGER tr_gr_updated BEFORE UPDATE ON goods_receipts
FOR EACH ROW EXECUTE FUNCTION update_procurement_advanced_timestamp();

DROP TRIGGER IF EXISTS tr_return_updated ON purchase_returns;
CREATE TRIGGER tr_return_updated BEFORE UPDATE ON purchase_returns
FOR EACH ROW EXECUTE FUNCTION update_procurement_advanced_timestamp();
