-- =====================================================
-- CONSTRUCTION MODULE - Forma 19 stage blocking flag
-- Migration: 245_stage_f19_blocking.sql
-- Adds requires_f19 flag to construction stages
-- When true, stage cannot be completed without a signed Forma 19
-- =====================================================

ALTER TABLE construction_stages ADD COLUMN IF NOT EXISTS requires_f19 BOOLEAN DEFAULT false;
