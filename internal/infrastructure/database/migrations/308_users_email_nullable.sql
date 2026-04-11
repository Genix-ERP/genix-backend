-- Allow users without email (phone-only employees)
ALTER TABLE users ALTER COLUMN email DROP NOT NULL;
