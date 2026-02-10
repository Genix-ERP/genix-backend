-- Add Google OAuth columns to users table

-- Make password_hash nullable (Google users won't have a password)
ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;

-- Add auth_provider column (default 'local' for existing users)
ALTER TABLE users ADD COLUMN IF NOT EXISTS auth_provider VARCHAR(20) DEFAULT 'local' NOT NULL;

-- Add google_id column (Google's unique subject identifier)
ALTER TABLE users ADD COLUMN IF NOT EXISTS google_id VARCHAR(255);

-- Index for fast Google ID lookup
CREATE INDEX IF NOT EXISTS idx_users_google_id ON users(google_id) WHERE google_id IS NOT NULL;
