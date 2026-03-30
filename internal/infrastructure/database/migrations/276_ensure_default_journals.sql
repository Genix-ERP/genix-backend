-- Ensure all default journals exist for every tenant
-- Only inserts if the journal code doesn't already exist for that tenant

INSERT INTO journals (id, tenant_id, code, name, name_uz, name_en, type, auto_sequence, next_number, number_prefix, is_active, created_at, updated_at)
SELECT gen_random_uuid(), t.id, j.code, j.name, j.name_uz, j.name_en, j.type, true, 1, j.prefix, true, NOW(), NOW()
FROM tenants t
CROSS JOIN (VALUES
    ('BANK',          'Банковский журнал',                'Bank jurnali',               'Bank Journal',            'bank',    'BANK'),
    ('CASH',          'Кассовый журнал',                  'Kassa jurnali',              'Cash Journal',            'cash',    'CASH'),
    ('CASH_RECEIPTS', 'Журнал кассовых поступлений',      'Naqd pul tushumlari jurnali', 'Cash Receipts Journal',  'cash',    'CR'),
    ('GEN',           'Главный журнал',                   'Bosh jurnal',                'General Journal',         'general', 'GEN'),
    ('MISC',          'Прочие операции',                  'Boshqa operatsiyalar jurnali', 'Miscellaneous Journal', 'general', 'MISC'),
    ('PUR',           'Журнал закупок',                   'Xarid jurnali',              'Purchase Journal',        'purchase','PUR'),
    ('SAL',           'Журнал продаж',                    'Sotish jurnali',             'Sales Journal',           'sale',    'SAL'),
    ('STOCK',         'Складской журнал',                 'Ombor jurnali',              'Stock Journal',           'general', 'STK'),
    ('ASSET',         'Журнал основных средств',          'Asosiy vositalar jurnali',   'Fixed Assets Journal',    'general', 'AST'),
    ('PAYROLL',       'Журнал зарплаты',                  'Ish haqi jurnali',           'Payroll Journal',         'general', 'PAY'),
    ('CONST',         'Строительный журнал',              'Qurilish jurnali',           'Construction Journal',    'general', 'CON')
) AS j(code, name, name_uz, name_en, type, prefix)
WHERE NOT EXISTS (
    SELECT 1 FROM journals WHERE journals.tenant_id = t.id AND journals.code = j.code AND journals.deleted_at IS NULL
);
