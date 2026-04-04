-- Migration: 297_fix_reconciliation_status_check.sql
-- Description: Fix CHECK constraint on reconciliation_acts.status to allow 'discrepancy' and 'no_response'
-- The original table (migration 003) had CHECK (status IN ('draft','sent','confirmed','disputed'))
-- but the application now uses 'discrepancy' and 'no_response' statuses as well.
-- Date: 2026-04-04

-- Drop the old restrictive CHECK constraint and add a new one with all valid statuses
DO $$
DECLARE
    constraint_name TEXT;
BEGIN
    -- Find the CHECK constraint on the status column
    SELECT con.conname INTO constraint_name
    FROM pg_constraint con
    JOIN pg_attribute att ON att.attnum = ANY(con.conkey) AND att.attrelid = con.conrelid
    WHERE con.conrelid = 'reconciliation_acts'::regclass
      AND con.contype = 'c'
      AND att.attname = 'status';

    -- Drop it if found
    IF constraint_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE reconciliation_acts DROP CONSTRAINT %I', constraint_name);
    END IF;
END
$$;

-- Add the updated CHECK constraint with all valid statuses
ALTER TABLE reconciliation_acts
ADD CONSTRAINT reconciliation_acts_status_check
CHECK (status IN ('draft', 'sent', 'confirmed', 'disputed', 'discrepancy', 'no_response'));
