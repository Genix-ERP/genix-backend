-- Email verification OTP table
CREATE TABLE IF NOT EXISTS email_verification_otps (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) NOT NULL,
    otp_code VARCHAR(6) NOT NULL,
    purpose VARCHAR(50) NOT NULL DEFAULT 'registration', -- registration, password_reset, email_change
    attempts INT DEFAULT 0,
    max_attempts INT DEFAULT 3,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    verified_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT unique_active_otp UNIQUE (email, purpose, verified_at)
);

-- Index for quick lookups
CREATE INDEX IF NOT EXISTS idx_email_verification_otps_email ON email_verification_otps(email);
CREATE INDEX IF NOT EXISTS idx_email_verification_otps_expires_at ON email_verification_otps(expires_at);

-- Grant permissions (for deployments where app user differs from migration user)
GRANT ALL PRIVILEGES ON TABLE email_verification_otps TO PUBLIC;

-- Cleanup old OTPs (can be run as a scheduled job)
-- DELETE FROM email_verification_otps WHERE expires_at < NOW() - INTERVAL '24 hours';
