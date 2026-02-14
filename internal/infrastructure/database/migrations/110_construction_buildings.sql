-- =====================================================
-- CONSTRUCTION MODULE - BUILDINGS/BLOCKS SUPPORT
-- Migration: 110_construction_buildings.sql
-- =====================================================

-- Construction Buildings/Blocks (Multiple buildings within a project)
CREATE TABLE IF NOT EXISTS construction_buildings (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    project_id BIGINT NOT NULL REFERENCES construction_projects(id) ON DELETE CASCADE,

    -- Building identification
    code VARCHAR(50) NOT NULL,
    name VARCHAR(500) NOT NULL,
    description TEXT,

    -- Building type
    building_type VARCHAR(100), -- residential, commercial, parking, etc.
    building_purpose VARCHAR(100), -- living, non-living, mixed

    -- Physical characteristics
    floors_count INTEGER,
    floors_underground INTEGER DEFAULT 0,
    total_area DECIMAL(15,2),
    living_area DECIMAL(15,2),
    non_living_area DECIMAL(15,2),

    -- Units info (for residential)
    apartments_count INTEGER,
    commercial_units_count INTEGER,
    parking_spots INTEGER,

    -- Financial
    estimated_cost DECIMAL(18,2),
    actual_cost DECIMAL(18,2) DEFAULT 0,
    currency VARCHAR(3) DEFAULT 'UZS',

    -- Timeline
    planned_start_date DATE,
    planned_end_date DATE,
    actual_start_date DATE,
    actual_end_date DATE,

    -- Status
    status VARCHAR(50) DEFAULT 'draft',
    progress_percent DECIMAL(5,2) DEFAULT 0,

    -- Location within project
    gps_coordinates JSONB,
    location_description TEXT,

    sort_order INTEGER DEFAULT 0,

    created_date TIMESTAMP DEFAULT NOW(),
    updated_date TIMESTAMP DEFAULT NOW(),

    UNIQUE(project_id, code)
);

-- Add building_id to smeta_sections to link sections to specific buildings
DO $$
BEGIN
    IF EXISTS (SELECT FROM information_schema.tables WHERE table_name = 'smeta_sections') THEN
        ALTER TABLE smeta_sections ADD COLUMN IF NOT EXISTS building_id BIGINT REFERENCES construction_buildings(id);
    END IF;
END $$;

-- Add building_id to other relevant tables (only if they exist)
DO $$
BEGIN
    IF EXISTS (SELECT FROM information_schema.tables WHERE table_name = 'construction_daily_logs') THEN
        ALTER TABLE construction_daily_logs ADD COLUMN IF NOT EXISTS building_id BIGINT REFERENCES construction_buildings(id);
    END IF;
END $$;

DO $$
BEGIN
    IF EXISTS (SELECT FROM information_schema.tables WHERE table_name = 'construction_photo_reports') THEN
        ALTER TABLE construction_photo_reports ADD COLUMN IF NOT EXISTS building_id BIGINT REFERENCES construction_buildings(id);
    END IF;
END $$;

DO $$
BEGIN
    IF EXISTS (SELECT FROM information_schema.tables WHERE table_name = 'construction_project_team') THEN
        ALTER TABLE construction_project_team ADD COLUMN IF NOT EXISTS building_id BIGINT REFERENCES construction_buildings(id);
    END IF;
END $$;

-- Update project fields for complex projects
ALTER TABLE construction_projects ADD COLUMN IF NOT EXISTS buildings_count INTEGER DEFAULT 0;
ALTER TABLE construction_projects ADD COLUMN IF NOT EXISTS total_apartments INTEGER DEFAULT 0;
ALTER TABLE construction_projects ADD COLUMN IF NOT EXISTS total_commercial_units INTEGER DEFAULT 0;
ALTER TABLE construction_projects ADD COLUMN IF NOT EXISTS total_parking_spots INTEGER DEFAULT 0;

-- Indexes
CREATE INDEX IF NOT EXISTS idx_construction_buildings_project ON construction_buildings(project_id);
CREATE INDEX IF NOT EXISTS idx_construction_buildings_status ON construction_buildings(status);
CREATE INDEX IF NOT EXISTS idx_smeta_sections_building ON smeta_sections(building_id);
