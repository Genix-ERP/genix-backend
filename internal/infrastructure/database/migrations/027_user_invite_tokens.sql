-- Add invite token columns to users table for invitation flow

-- Add invite_token and invite_token_expires columns
ALTER TABLE users ADD COLUMN IF NOT EXISTS invite_token VARCHAR(255);
ALTER TABLE users ADD COLUMN IF NOT EXISTS invite_token_expires TIMESTAMP WITH TIME ZONE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS invited_by UUID REFERENCES users(id);
ALTER TABLE users ADD COLUMN IF NOT EXISTS invited_at TIMESTAMP WITH TIME ZONE;

-- Index for fast token lookup
CREATE INDEX IF NOT EXISTS idx_users_invite_token ON users(invite_token) WHERE invite_token IS NOT NULL;

-- Comment
COMMENT ON COLUMN users.invite_token IS 'Unique token for user invitation, used to set initial password';
COMMENT ON COLUMN users.invite_token_expires IS 'Expiration time for the invite token';
COMMENT ON COLUMN users.invited_by IS 'User who sent the invitation';
COMMENT ON COLUMN users.invited_at IS 'When the invitation was sent';
