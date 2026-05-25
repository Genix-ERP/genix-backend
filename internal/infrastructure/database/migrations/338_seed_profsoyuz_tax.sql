-- 338_seed_profsoyuz_tax.sql
-- Seed the Profsoyuz (trade-union dues) tax row into every tenant's
-- employee_taxes catalog. 1% of gross, employee-paid.
-- See §3.2 of ТЗ_Ish_Haqi_Soliq_Tolik.docx.
--
-- Disabled by default (is_active = FALSE) for consistency with the other
-- seeded rows from migration 330 — admin has to pick a GL account and flip
-- it on before it will be applied to payroll entries. The WHERE NOT EXISTS
-- guard makes this migration idempotent per-tenant: if the code already
-- exists for a tenant (perhaps the admin pre-created it), we leave it
-- alone rather than forcing a duplicate or overwriting their settings.

INSERT INTO employee_taxes (
    id, tenant_id, organization_id,
    code, name, description,
    rate, base_type, payer,
    is_active, sort_order,
    created_at, updated_at
)
SELECT
    uuid_generate_v4(), t.id, NULL,
    'PROFSOYUZ',
    'Kasaba uyushmasi badali (Profsoyuz)',
    'Kasaba uyushmasi a''zoligi uchun majburiy badal. 1% — brutto maoshdan ushlab qolinadi. Xodim kasaba uyushmasi a''zosi bo''lgan taqdirda.',
    1.0000, 'gross', 'employee',
    FALSE, 15,   -- sort between NDFL (10) and INPS (20)
    NOW(), NOW()
FROM tenants t
WHERE NOT EXISTS (
    SELECT 1 FROM employee_taxes et
    WHERE et.tenant_id = t.id
      AND LOWER(et.code) = 'profsoyuz'
      AND et.deleted_at IS NULL
);
