-- Migration: 299_construction_act_types.sql
-- Description: Create construction_act_types table to allow users to define custom act types.
--              Seed with the 5 default system act types.
--              Also widen act_type column in construction_act from VARCHAR(20) to VARCHAR(100).
-- Date: 2026-04-04

-- Create the act types table
CREATE TABLE IF NOT EXISTS construction_act_types (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL,
    value VARCHAR(100) NOT NULL,
    label VARCHAR(100) NOT NULL,
    color VARCHAR(50) DEFAULT 'bg-slate-100 text-slate-700',
    is_system BOOLEAN DEFAULT false,
    sort_order INT DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_act_types_tenant_value
    ON construction_act_types(tenant_id, value);
CREATE INDEX IF NOT EXISTS idx_act_types_tenant
    ON construction_act_types(tenant_id);

-- Widen act_type column to support longer custom type names
ALTER TABLE construction_act ALTER COLUMN act_type TYPE VARCHAR(100);
