-- Migration: 133_stock_counts_counted_by.sql
-- Description: Add counted_by text column to stock_counts for storing counter name
-- Date: 2026-02-25

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'stock_counts' AND column_name = 'counted_by_name'
    ) THEN
        ALTER TABLE stock_counts ADD COLUMN counted_by_name VARCHAR(255);
    END IF;
END
$$;
