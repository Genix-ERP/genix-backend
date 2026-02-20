-- Migration: 126_fix_reconciliation_acts_schema.sql
-- Description: Fix reconciliation_acts table schema - rename generated 'difference' column to 'discrepancy',
--              add missing columns to reconciliation_lines
-- Date: 2026-02-19

-- ============================================
-- FIX reconciliation_acts: drop generated 'difference' column, add 'discrepancy'
-- ============================================
-- The table was originally created in migration 003 with 'difference' as a GENERATED column.
-- Migration 125 tried to recreate with 'discrepancy' but used IF NOT EXISTS so it was a no-op.
-- The handler code references 'discrepancy', so we need to fix the schema.

DO $$
BEGIN
    -- Drop the generated 'difference' column if it exists
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'reconciliation_acts' AND column_name = 'difference'
    ) THEN
        ALTER TABLE reconciliation_acts DROP COLUMN difference;
    END IF;

    -- Add 'discrepancy' column if it doesn't exist
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'reconciliation_acts' AND column_name = 'discrepancy'
    ) THEN
        ALTER TABLE reconciliation_acts ADD COLUMN discrepancy DECIMAL(20, 4) DEFAULT 0;
    END IF;
END
$$;

-- ============================================
-- FIX reconciliation_lines: add missing columns from migration 125
-- ============================================
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'reconciliation_lines' AND column_name = 'discrepancy'
    ) THEN
        ALTER TABLE reconciliation_lines ADD COLUMN discrepancy DECIMAL(20, 4) DEFAULT 0;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'reconciliation_lines' AND column_name = 'is_disputed'
    ) THEN
        ALTER TABLE reconciliation_lines ADD COLUMN is_disputed BOOLEAN DEFAULT false;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'reconciliation_lines' AND column_name = 'dispute_note'
    ) THEN
        ALTER TABLE reconciliation_lines ADD COLUMN dispute_note TEXT;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'reconciliation_lines' AND column_name = 'updated_at'
    ) THEN
        ALTER TABLE reconciliation_lines ADD COLUMN updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP;
    END IF;
END
$$;

-- ============================================
-- Remove CHECK constraint on status if it's too restrictive
-- (migration 003 added CHECK, migration 125 didn't - keep it as-is, it's fine)
-- ============================================
