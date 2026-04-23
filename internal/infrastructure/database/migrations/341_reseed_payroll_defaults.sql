-- 341_reseed_payroll_defaults.sql
-- Reverses the intent of migration 331 for the three payroll taxes the
-- TZ §1.2 lists as defaults — NDFL / INPS / SOC_TAX (ЕСП). Migration 331
-- deleted them because an earlier product decision was "admins will
-- define their own". That decision has since been reversed: the TZ
-- explicitly requires the full 8-tax default catalog (migration 340 added
-- the 4 company-level taxes; this migration restores the 3 payroll-level
-- taxes that were swept out in 331).
--
-- PROFSOYUZ is NOT re-seeded here — migration 338 already covers it.
--
-- Idempotent: `WHERE NOT EXISTS` per (tenant, code) prevents duplicates
-- for tenants who already have the row (either because they were created
-- before 331 ran, or because an admin re-added it manually).
--
-- Seeded as is_active = FALSE, account_id = NULL — same shape as the
-- original migration 330 seed — so the admin still has to pick a GL
-- account and flip the row on before anything posts to the journal.

INSERT INTO employee_taxes (
    id, tenant_id, organization_id,
    code, name, description,
    rate, base_type, payer,
    is_active, sort_order,
    created_at, updated_at
)
SELECT
    uuid_generate_v4(), t.id, NULL,
    s.code, s.name, s.description,
    s.rate, s.base_type, s.payer,
    FALSE, s.sort_order,
    NOW(), NOW()
FROM tenants t
CROSS JOIN (VALUES
    ('NDFL',    'Daromad solig''i (NDFL)',
       'Jismoniy shaxslardan olinadigan daromad solig''i. 12% — brutto maoshdan ushlab qolinadi.',
       12.0000, 'gross', 'employee', 10),
    ('INPS',    'INPS (Ijtimoiy sug''urta jamg''armasi)',
       'Jismoniy shaxsning ijtimoiy sug''urta jamg''armasiga majburiy badali. 0.1% — brutto maoshdan ushlab qolinadi.',
        0.1000, 'gross', 'employee', 20),
    ('SOC_TAX', 'Ijtimoiy soliq (ish beruvchi)',
       'Ish beruvchi tomonidan to''lanadigan ijtimoiy soliq (ЕСП). 12% — xodim maoshidan ushlab qolinmaydi, kompaniya xarajati.',
       12.0000, 'gross', 'employer', 30)
) AS s(code, name, description, rate, base_type, payer, sort_order)
WHERE NOT EXISTS (
    SELECT 1 FROM employee_taxes et
    WHERE et.tenant_id = t.id
      AND LOWER(et.code) = LOWER(s.code)
      AND et.deleted_at IS NULL
);
