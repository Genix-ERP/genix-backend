-- Migration: 056_missing_columns_fix.sql
-- Description: Add all missing columns that are causing errors in production
-- Date: 2026-02-02
-- Note: Uses conditional checks for tables that may not exist in all environments

-- =====================================================
-- SALES ORDERS - Missing columns (always exists)
-- =====================================================

-- Add carrier column for shipping carrier selection
ALTER TABLE sales_orders ADD COLUMN IF NOT EXISTS carrier VARCHAR(255);

-- Add shipping_cost column for delivery costs
ALTER TABLE sales_orders ADD COLUMN IF NOT EXISTS shipping_cost DECIMAL(20, 4) DEFAULT 0;

-- Add delivery_date column for expected delivery
ALTER TABLE sales_orders ADD COLUMN IF NOT EXISTS delivery_date DATE;

-- =====================================================
-- WAREHOUSE LOCATIONS - Missing soft delete column (always exists)
-- =====================================================

ALTER TABLE warehouse_locations ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;
CREATE INDEX IF NOT EXISTS idx_warehouse_locations_deleted_at ON warehouse_locations(deleted_at);

-- =====================================================
-- OTHER TABLES - Conditional soft delete columns
-- =====================================================

-- Helper function to safely add deleted_at column
DO $$
DECLARE
    tables_to_update TEXT[] := ARRAY[
        'inventory',
        'stock_movements',
        'purchase_invoices',
        'employees',
        'leave_requests',
        'attendance_records',
        'projects',
        'manufacturing_orders',
        'bill_of_materials',
        'leads',
        'opportunities',
        'activities',
        'tasks',
        'quotations',
        'sales_returns',
        'purchase_returns',
        'goods_receipts',
        'purchase_requisitions',
        'carriers',
        'sales_delivery_orders',
        'discounts'
    ];
    tbl TEXT;
BEGIN
    FOREACH tbl IN ARRAY tables_to_update
    LOOP
        IF EXISTS (SELECT FROM information_schema.tables WHERE table_name = tbl AND table_schema = 'public') THEN
            EXECUTE format('ALTER TABLE %I ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE', tbl);
            EXECUTE format('CREATE INDEX IF NOT EXISTS idx_%s_deleted_at ON %I(deleted_at)', tbl, tbl);
        END IF;
    END LOOP;
END $$;
