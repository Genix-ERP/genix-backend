-- Migration: Purchase-Finance Integration
-- Adds columns to link purchase invoices with purchase orders and goods receipts

-- Ensure goods_receipts table exists (may not exist if 005_procurement_advanced wasn't applied)
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

-- Add purchase_order_id column to purchase_invoices
ALTER TABLE purchase_invoices ADD COLUMN IF NOT EXISTS purchase_order_id UUID REFERENCES purchase_orders(id);

-- Add goods_receipt_id column to purchase_invoices
ALTER TABLE purchase_invoices ADD COLUMN IF NOT EXISTS goods_receipt_id UUID REFERENCES goods_receipts(id);

-- Add three_way_match_status column for validation
ALTER TABLE purchase_invoices ADD COLUMN IF NOT EXISTS three_way_match_status VARCHAR(20) DEFAULT 'pending';
-- Values: pending, matched, mismatch

-- Add paid_amount column if not exists (for partial payment tracking)
ALTER TABLE purchase_invoices ADD COLUMN IF NOT EXISTS paid_amount DECIMAL(20, 4) DEFAULT 0;

-- Create indexes for fast lookup
CREATE INDEX IF NOT EXISTS idx_purchase_invoices_po_id ON purchase_invoices(purchase_order_id);
CREATE INDEX IF NOT EXISTS idx_purchase_invoices_gr_id ON purchase_invoices(goods_receipt_id);

-- Add supplier_id and supplier_name aliases (frontend uses supplier, backend uses vendor)
-- These are nullable since existing records may not have them
ALTER TABLE purchase_invoices ADD COLUMN IF NOT EXISTS supplier_id UUID REFERENCES contacts(id);
ALTER TABLE purchase_invoices ADD COLUMN IF NOT EXISTS supplier_name VARCHAR(255);

-- Copy vendor_id to supplier_id where supplier_id is null
UPDATE purchase_invoices SET supplier_id = vendor_id WHERE supplier_id IS NULL AND vendor_id IS NOT NULL;
