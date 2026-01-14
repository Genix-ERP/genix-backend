-- Migration: 004_soft_delete_columns.sql
-- Description: Add missing deleted_at columns for soft delete functionality
-- Date: 2026-01-14

-- =====================================================
-- ADD SOFT DELETE COLUMNS TO EXISTING TABLES
-- =====================================================

-- These tables are missing the deleted_at column required for soft delete
-- functionality, causing "column does not exist" errors in queries

-- 1. Journal Entries - Finance module
ALTER TABLE journal_entries ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;
CREATE INDEX IF NOT EXISTS idx_journal_entries_deleted_at ON journal_entries(deleted_at);

-- 2. Tax Rates - Finance module
ALTER TABLE tax_rates ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;
CREATE INDEX IF NOT EXISTS idx_tax_rates_deleted_at ON tax_rates(deleted_at);

-- 3. Product Categories - Inventory module
ALTER TABLE product_categories ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;
CREATE INDEX IF NOT EXISTS idx_product_categories_deleted_at ON product_categories(deleted_at);

-- 4. Warehouses - Inventory module
ALTER TABLE warehouses ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;
CREATE INDEX IF NOT EXISTS idx_warehouses_deleted_at ON warehouses(deleted_at);

-- 5. Payments - Finance module
ALTER TABLE payments ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;
CREATE INDEX IF NOT EXISTS idx_payments_deleted_at ON payments(deleted_at);

-- =====================================================
-- ADDITIONAL TABLES THAT MAY NEED SOFT DELETE
-- =====================================================

-- Adding deleted_at to other core tables for consistency
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;
CREATE INDEX IF NOT EXISTS idx_contacts_deleted_at ON contacts(deleted_at);

ALTER TABLE products ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;
CREATE INDEX IF NOT EXISTS idx_products_deleted_at ON products(deleted_at);

ALTER TABLE accounts ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;
CREATE INDEX IF NOT EXISTS idx_accounts_deleted_at ON accounts(deleted_at);

ALTER TABLE invoices ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;
CREATE INDEX IF NOT EXISTS idx_invoices_deleted_at ON invoices(deleted_at);

ALTER TABLE purchase_orders ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;
CREATE INDEX IF NOT EXISTS idx_purchase_orders_deleted_at ON purchase_orders(deleted_at);

ALTER TABLE sales_orders ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;
CREATE INDEX IF NOT EXISTS idx_sales_orders_deleted_at ON sales_orders(deleted_at);
