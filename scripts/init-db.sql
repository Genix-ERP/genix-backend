-- GenixERP Database Initialization Script
-- This script runs automatically when PostgreSQL container starts for the first time

-- Create required extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE EXTENSION IF NOT EXISTS "pg_trgm";

-- Grant privileges
GRANT ALL PRIVILEGES ON DATABASE genixerp TO genix;

-- Log initialization
DO $$
BEGIN
    RAISE NOTICE 'GenixERP database initialized successfully';
END $$;
