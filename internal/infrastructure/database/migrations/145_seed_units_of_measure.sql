-- Migration 144: Seed default units of measure for all tenants
-- These match the hardcoded UOM options in the frontend Products form

-- Insert default UOM entries for every existing tenant
INSERT INTO units_of_measure (id, tenant_id, code, name, category, conversion_factor, is_active)
SELECT uuid_generate_v4(), t.id, u.code, u.name, u.category, u.factor, true
FROM tenants t
CROSS JOIN (VALUES
    ('unit',  'Dona',        'quantity', 1),
    ('kg',    'Kilogramm',   'weight',   1),
    ('g',     'Gramm',       'weight',   0.001),
    ('l',     'Litr',        'volume',   1),
    ('ml',    'Millilitr',   'volume',   0.001),
    ('m',     'Metr',        'length',   1),
    ('cm',    'Santimetr',   'length',   0.01),
    ('box',   'Quti',        'quantity', 1),
    ('pack',  'Paket',       'quantity', 1),
    ('dozen', 'Dujina',      'quantity', 12)
) AS u(code, name, category, factor)
WHERE NOT EXISTS (
    SELECT 1 FROM units_of_measure uom
    WHERE uom.tenant_id = t.id AND uom.code = u.code
);
