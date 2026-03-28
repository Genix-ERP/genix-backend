-- Backfill tender_company_profiles for existing tender_users who don't have one
INSERT INTO tender_company_profiles (id, user_id, role, company_name, inn, phone, region_id)
SELECT
    u.id,
    u.id,
    u.role,
    COALESCE(NULLIF(u.company_name, ''), u.full_name),
    COALESCE(u.inn, ''),
    COALESCE(u.phone, ''),
    u.region_id
FROM tender_users u
WHERE NOT EXISTS (
    SELECT 1 FROM tender_company_profiles cp WHERE cp.user_id = u.id AND cp.deleted_at IS NULL
);
