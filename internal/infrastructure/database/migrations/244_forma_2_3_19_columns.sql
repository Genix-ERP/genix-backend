-- =====================================================
-- CONSTRUCTION MODULE - Forma 2, Forma 3, Forma 19 columns
-- Migration: 244_forma_2_3_19_columns.sql
-- Extends construction_act and construction_act_line
-- for enhanced KS-2, KS-3, and hidden works reporting
-- =====================================================

-- Forma 2 (KS-2) enhancements: VAT + electronic signing
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS act_number INT;
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS vat_pct DECIMAL(5,2) DEFAULT 12;
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS vat_amount DECIMAL(18,2) DEFAULT 0;
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS amount_total_with_vat DECIMAL(18,2) DEFAULT 0;
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS signed_contractor_at TIMESTAMPTZ;
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS signed_contractor_by UUID;
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS signed_client_at TIMESTAMPTZ;
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS signed_client_by UUID;

-- Forma 19 (hidden works) fields
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS stage_id BIGINT REFERENCES construction_stages(id) ON DELETE SET NULL;
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS location_axes TEXT;
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS drawing_reference TEXT;
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS works_start_date DATE;
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS works_end_date DATE;
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS photos JSONB DEFAULT '[]';
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS materials_json JSONB DEFAULT '[]';
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS signed_designer_at TIMESTAMPTZ;
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS signed_designer_by UUID;
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS signed_gasn_at TIMESTAMPTZ;
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS signed_gasn_by UUID;

-- Forma 3 (KS-3) cumulative tracking columns
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS cumul_from_start DECIMAL(18,2) DEFAULT 0;
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS cumul_from_year_start DECIMAL(18,2) DEFAULT 0;
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS cumul_previous_period DECIMAL(18,2) DEFAULT 0;
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS smr_amount DECIMAL(18,2) DEFAULT 0;
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS equipment_amount DECIMAL(18,2) DEFAULT 0;
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS other_amount DECIMAL(18,2) DEFAULT 0;

-- Act line enhancements for Forma 2
ALTER TABLE construction_act_line ADD COLUMN IF NOT EXISTS qty_smeta DECIMAL(18,4) DEFAULT 0;
ALTER TABLE construction_act_line ADD COLUMN IF NOT EXISTS note TEXT;

-- Indexes
CREATE INDEX IF NOT EXISTS idx_act_stage ON construction_act(stage_id);
