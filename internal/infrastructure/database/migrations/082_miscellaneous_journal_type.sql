-- Add default miscellaneous journal for existing tenants that don't have one
INSERT INTO journals (id, tenant_id, code, name, type, is_active, auto_sequence, next_number, created_at)
SELECT uuid_generate_v4(), t.id, 'MISC', 'Miscellaneous Journal', 'miscellaneous', true, true, 1, CURRENT_TIMESTAMP
FROM tenants t
WHERE NOT EXISTS (
    SELECT 1 FROM journals j WHERE j.tenant_id = t.id AND j.code = 'MISC'
);
