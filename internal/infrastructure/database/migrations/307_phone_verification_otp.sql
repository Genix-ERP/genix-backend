-- Phone-based OTP verification for password set/reset
CREATE TABLE IF NOT EXISTS phone_verification_otps (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    phone VARCHAR(20) NOT NULL,
    otp_code VARCHAR(6) NOT NULL,
    purpose VARCHAR(50) NOT NULL DEFAULT 'password_reset',
    attempts INT DEFAULT 0,
    max_attempts INT DEFAULT 3,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    verified_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_phone_otp_phone_purpose ON phone_verification_otps (phone, purpose);
CREATE INDEX IF NOT EXISTS idx_phone_otp_expires ON phone_verification_otps (expires_at);
