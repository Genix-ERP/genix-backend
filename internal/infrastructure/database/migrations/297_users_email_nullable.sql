-- Allow phone-only registration: make email nullable in users table
ALTER TABLE users ALTER COLUMN email DROP NOT NULL;
