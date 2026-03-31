-- Fix: Ensure default units of measure exist for ALL tenants
-- Root cause: Migration 145 seeded UoMs for tenants that existed at the time,
-- but tenants created after that migration have no UoMs.

INSERT INTO units_of_measure (id, tenant_id, code, name, category, conversion_factor, is_active)
SELECT gen_random_uuid(), t.id, u.code, u.name, u.category, u.factor, true
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
    ('dozen', 'Dujina',      'quantity', 12),
    ('t',     'Tonna',       'weight',   1000),
    ('pcs',   'Dona (pcs)',  'quantity', 1),
    ('set',   'To''plam',    'quantity', 1),
    ('pair',  'Juft',        'quantity', 2),
    ('roll',  'Rulon',       'quantity', 1),
    ('sheet', 'Varaq',       'quantity', 1),
    ('m2',    'Kvadrat metr','area',     1),
    ('m3',    'Kub metr',    'volume',   1000)
) AS u(code, name, category, factor)
WHERE NOT EXISTS (
    SELECT 1 FROM units_of_measure uom
    WHERE uom.tenant_id = t.id AND uom.code = u.code
);
