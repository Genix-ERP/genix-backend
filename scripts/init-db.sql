-- GenixERP Database Initialization Script
-- This script runs automatically when PostgreSQL container starts for the first time
-- It runs on the database specified by POSTGRES_DB (genixerp)

-- Create required extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE EXTENSION IF NOT EXISTS "pg_trgm";

-- Log initialization
DO $$
BEGIN
    RAISE NOTICE 'GenixERP database initialized successfully';
END $$;
