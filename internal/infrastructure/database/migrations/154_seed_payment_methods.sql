-- Seed default payment methods for existing tenants that don't have them yet

INSERT INTO payment_methods (id, tenant_id, code, name, type, is_active, created_at)
SELECT gen_random_uuid(), t.id, pm.code, pm.name, pm.type, true, NOW()
FROM tenants t
CROSS JOIN (VALUES
    ('CASH', 'Cash', 'cash'),
    ('BANK', 'Bank Transfer', 'bank_transfer'),
    ('CARD', 'Credit Card', 'credit_card'),
    ('CHECK', 'Check', 'check')
) AS pm(code, name, type)
WHERE NOT EXISTS (
    SELECT 1 FROM payment_methods WHERE tenant_id = t.id AND code = pm.code
);

-- Link Cash payment method to Cash journals (inbound + outbound)
INSERT INTO journal_payment_methods (id, tenant_id, journal_id, payment_method_id, direction, name, is_active, created_at)
SELECT gen_random_uuid(), j.tenant_id, j.id, pm.id, d.dir, '', true, NOW()
FROM journals j
JOIN payment_methods pm ON pm.tenant_id = j.tenant_id AND pm.code = 'CASH'
CROSS JOIN (VALUES ('inbound'), ('outbound')) AS d(dir)
WHERE j.code = 'CASH' AND j.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM journal_payment_methods jpm
      WHERE jpm.journal_id = j.id AND jpm.payment_method_id = pm.id AND jpm.direction = d.dir
  );

-- Link Bank Transfer to Bank journals (inbound + outbound)
INSERT INTO journal_payment_methods (id, tenant_id, journal_id, payment_method_id, direction, name, is_active, created_at)
SELECT gen_random_uuid(), j.tenant_id, j.id, pm.id, d.dir, '', true, NOW()
FROM journals j
JOIN payment_methods pm ON pm.tenant_id = j.tenant_id AND pm.code = 'BANK'
CROSS JOIN (VALUES ('inbound'), ('outbound')) AS d(dir)
WHERE j.code = 'BANK' AND j.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM journal_payment_methods jpm
      WHERE jpm.journal_id = j.id AND jpm.payment_method_id = pm.id AND jpm.direction = d.dir
  );

-- Link Credit Card to Bank journals (inbound only)
INSERT INTO journal_payment_methods (id, tenant_id, journal_id, payment_method_id, direction, name, is_active, created_at)
SELECT gen_random_uuid(), j.tenant_id, j.id, pm.id, 'inbound', '', true, NOW()
FROM journals j
JOIN payment_methods pm ON pm.tenant_id = j.tenant_id AND pm.code = 'CARD'
WHERE j.code = 'BANK' AND j.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM journal_payment_methods jpm
      WHERE jpm.journal_id = j.id AND jpm.payment_method_id = pm.id AND jpm.direction = 'inbound'
  );

-- Link Check to Bank journals (inbound + outbound)
INSERT INTO journal_payment_methods (id, tenant_id, journal_id, payment_method_id, direction, name, is_active, created_at)
SELECT gen_random_uuid(), j.tenant_id, j.id, pm.id, d.dir, '', true, NOW()
FROM journals j
JOIN payment_methods pm ON pm.tenant_id = j.tenant_id AND pm.code = 'CHECK'
CROSS JOIN (VALUES ('inbound'), ('outbound')) AS d(dir)
WHERE j.code = 'BANK' AND j.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM journal_payment_methods jpm
      WHERE jpm.journal_id = j.id AND jpm.payment_method_id = pm.id AND jpm.direction = d.dir
  );
