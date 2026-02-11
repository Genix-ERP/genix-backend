-- Fix: Add is_winner column to rfq_responses table
-- Migration 052 tried to create the table with is_winner, but since the table
-- already existed from migration 015, the column was never added.

ALTER TABLE rfq_responses ADD COLUMN IF NOT EXISTS is_winner BOOLEAN DEFAULT FALSE;
