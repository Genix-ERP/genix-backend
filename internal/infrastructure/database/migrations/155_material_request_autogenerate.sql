-- =====================================================
-- Migration 155: Material Request - auto-generate number + product fields
-- =====================================================

-- Make request_number nullable so backend can auto-generate it
ALTER TABLE construction_material_requests
    ALTER COLUMN request_number DROP NOT NULL;

-- Add a sequence per project for auto-numbering
CREATE SEQUENCE IF NOT EXISTS construction_material_request_seq;

-- Add expense_recorded flag so we don't double-record on re-confirm
ALTER TABLE construction_material_requests
    ADD COLUMN IF NOT EXISTS expense_recorded BOOLEAN DEFAULT false;
