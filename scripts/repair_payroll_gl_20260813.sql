-- ============================================================================
-- Payroll GL repair — moliya chuqur audit 2026-08-13
--
-- Repairs the two data-level consequences of the bugs fixed in code today:
--
--   A) Periods PAID while never processed: the payment JE debited 6710
--      although no accrual ever credited it. The salary expense is missing
--      from every P&L report and 6710 carries a phantom debit.
--      Repair: Dt 9420 / Kt 6710 per such payment (source_type =
--      'payroll_repair', reference = 'unaccrued_paid').
--
--   B) Accruals posted WITHOUT the withheld-tax split (empty
--      payroll_entry_taxes, legacy income_tax/social_security/pension
--      columns populated): 6710 was credited at GROSS while payments debit
--      NET, so the withheld tax sits on 6710 forever and 6410 never saw it.
--      Repair: Dt 6710 / Kt 6410 for the legacy tax sum per accrued period
--      (source_type = 'payroll_repair', reference = 'legacy_tax_split').
--
-- HOW TO USE
--   1. Preview:  run the two SELECT blocks marked "PREVIEW" first.
--   2. Apply:    psql ... -f scripts/repair_payroll_gl_20260813.sql
--   3. The script is idempotent: a period that already has a
--      'payroll_repair' JE with the same reference is skipped.
--
-- The new JEs are dated TODAY (not backdated) so closed reporting periods
-- are not rewritten; adjust entry_date manually if backdating is preferred.
-- ============================================================================

BEGIN;

-- ---------------------------------------------------------------------------
-- PREVIEW A — unaccrued paid periods (run alone to inspect before applying)
-- ---------------------------------------------------------------------------
-- SELECT je.tenant_id, pp.period_name, je.total_debit
-- FROM journal_entries je
-- JOIN payroll_periods pp ON pp.id = je.source_id
-- WHERE je.source_type = 'payroll_payment' AND je.status = 'posted'
--   AND je.deleted_at IS NULL AND je.reversed_entry_id IS NULL
--   AND EXISTS (SELECT 1 FROM journal_entry_lines l JOIN accounts a ON a.id = l.account_id
--               WHERE l.journal_entry_id = je.id AND l.debit_amount > 0 AND a.code = '6710')
--   AND NOT EXISTS (SELECT 1 FROM journal_entries ac WHERE ac.tenant_id = je.tenant_id
--                   AND ac.source_type = 'payroll' AND ac.source_id = je.source_id
--                   AND ac.status = 'posted' AND ac.deleted_at IS NULL);

-- ---------------------------------------------------------------------------
-- A) Dt 9420 / Kt 6710 for every unaccrued paid period
-- ---------------------------------------------------------------------------
WITH bad AS (
    SELECT je.id            AS payment_je_id,
           je.tenant_id,
           je.organization_id,
           je.journal_id,
           je.source_id     AS period_id,
           je.total_debit   AS amount,
           je.created_by,
           (SELECT l.account_id FROM journal_entry_lines l
             JOIN accounts a ON a.id = l.account_id
            WHERE l.journal_entry_id = je.id AND l.debit_amount > 0 AND a.code = '6710'
            LIMIT 1)        AS acct_6710
    FROM journal_entries je
    WHERE je.source_type = 'payroll_payment' AND je.status = 'posted'
      AND je.deleted_at IS NULL AND je.reversed_entry_id IS NULL
      AND EXISTS (SELECT 1 FROM payroll_periods pp WHERE pp.id = je.source_id)
      AND EXISTS (SELECT 1 FROM journal_entry_lines l JOIN accounts a ON a.id = l.account_id
                  WHERE l.journal_entry_id = je.id AND l.debit_amount > 0 AND a.code = '6710')
      AND NOT EXISTS (SELECT 1 FROM journal_entries ac
                      WHERE ac.tenant_id = je.tenant_id AND ac.source_type = 'payroll'
                        AND ac.source_id = je.source_id AND ac.status = 'posted'
                        AND ac.deleted_at IS NULL)
      AND NOT EXISTS (SELECT 1 FROM journal_entries r
                      WHERE r.tenant_id = je.tenant_id AND r.source_type = 'payroll_repair'
                        AND r.source_id = je.source_id AND r.reference = 'unaccrued_paid'
                        AND r.deleted_at IS NULL)
),
resolved AS (
    SELECT b.*,
           (SELECT a.id FROM accounts a
            WHERE a.tenant_id = b.tenant_id
              AND a.organization_id IS NOT DISTINCT FROM b.organization_id
              AND a.code = '9420' AND a.deleted_at IS NULL AND a.is_active = true
              AND COALESCE(a.is_leaf, true) = true
            LIMIT 1) AS acct_9420,
           row_number() OVER (ORDER BY b.payment_je_id) AS rn
    FROM bad b
    WHERE b.acct_6710 IS NOT NULL
),
ins AS (
    INSERT INTO journal_entries (
        id, tenant_id, organization_id, journal_id, entry_number, entry_date,
        reference, description, source_type, source_id, exchange_rate,
        total_debit, total_credit, status, created_by, created_at, updated_at)
    SELECT uuid_generate_v4(), r.tenant_id, r.organization_id, r.journal_id,
           'PAYFIX' || lpad((r.rn + (SELECT COUNT(*) FROM journal_entries x
                                     WHERE x.entry_number LIKE 'PAYFIX%'))::text, 5, '0'),
           CURRENT_DATE,
           'unaccrued_paid',
           'Repair: cash-basis salary expense (accrual never posted)',
           'payroll_repair', r.period_id, 1.0,
           r.amount, r.amount, 'posted', r.created_by, NOW(), NOW()
    FROM resolved r
    WHERE r.acct_9420 IS NOT NULL
    RETURNING id, source_id
),
line_dr AS (
    INSERT INTO journal_entry_lines (
        id, journal_entry_id, line_number, account_id, description,
        debit_amount, credit_amount, exchange_rate, created_at)
    SELECT uuid_generate_v4(), i.id, 1, r.acct_9420,
           'Salary Expense (repair)', r.amount, 0, 1.0, NOW()
    FROM ins i JOIN resolved r ON r.period_id = i.source_id
    RETURNING id
)
INSERT INTO journal_entry_lines (
    id, journal_entry_id, line_number, account_id, description,
    debit_amount, credit_amount, exchange_rate, created_at)
SELECT uuid_generate_v4(), i.id, 2, r.acct_6710,
       'Wages Payable (repair)', 0, r.amount, 1.0, NOW()
FROM ins i JOIN resolved r ON r.period_id = i.source_id;

-- keep the current_balance cache consistent (convention: += debit − credit)
UPDATE accounts a SET current_balance = current_balance + s.delta, updated_at = NOW()
FROM (
    SELECT l.account_id, SUM(l.debit_amount - l.credit_amount) AS delta
    FROM journal_entry_lines l
    JOIN journal_entries je ON je.id = l.journal_entry_id
    WHERE je.source_type = 'payroll_repair' AND je.reference = 'unaccrued_paid'
      AND je.created_at > NOW() - interval '1 minute'
    GROUP BY l.account_id
) s
WHERE a.id = s.account_id;

-- ---------------------------------------------------------------------------
-- PREVIEW B — accrued periods missing the withheld-tax split
-- ---------------------------------------------------------------------------
-- SELECT je.tenant_id, pp.period_name,
--        (SELECT COALESCE(SUM(COALESCE(pe.income_tax,0)+COALESCE(pe.social_security,0)+COALESCE(pe.pension,0)),0)
--         FROM payroll_entries pe WHERE pe.payroll_period_id = pp.id AND pe.deleted_at IS NULL) AS legacy_tax
-- FROM journal_entries je
-- JOIN payroll_periods pp ON pp.id = je.source_id
-- WHERE je.source_type = 'payroll' AND je.status = 'posted' AND je.deleted_at IS NULL
--   AND NOT EXISTS (SELECT 1 FROM journal_entry_lines l JOIN accounts a ON a.id = l.account_id
--                   WHERE l.journal_entry_id = je.id AND l.credit_amount > 0 AND a.code LIKE '64%');

-- ---------------------------------------------------------------------------
-- B) Dt 6710 / Kt 6410 for the legacy withheld tax of gross-credited accruals
-- ---------------------------------------------------------------------------
WITH accrued AS (
    SELECT je.id AS accrual_je_id, je.tenant_id, je.organization_id, je.journal_id,
           je.source_id AS period_id, je.created_by,
           (SELECT l.account_id FROM journal_entry_lines l
             JOIN accounts a ON a.id = l.account_id
            WHERE l.journal_entry_id = je.id AND l.credit_amount > 0 AND a.code = '6710'
            LIMIT 1) AS acct_6710
    FROM journal_entries je
    WHERE je.source_type = 'payroll' AND je.status = 'posted' AND je.deleted_at IS NULL
      AND NOT EXISTS (SELECT 1 FROM journal_entry_lines l JOIN accounts a ON a.id = l.account_id
                      WHERE l.journal_entry_id = je.id AND l.credit_amount > 0 AND a.code LIKE '64%')
      AND NOT EXISTS (SELECT 1 FROM journal_entries r
                      WHERE r.tenant_id = je.tenant_id AND r.source_type = 'payroll_repair'
                        AND r.source_id = je.source_id AND r.reference = 'legacy_tax_split'
                        AND r.deleted_at IS NULL)
),
taxed AS (
    SELECT ac.*,
           (SELECT COALESCE(SUM(COALESCE(pe.income_tax,0)+COALESCE(pe.social_security,0)+COALESCE(pe.pension,0)),0)
            FROM payroll_entries pe
            WHERE pe.payroll_period_id = ac.period_id AND pe.deleted_at IS NULL) AS legacy_tax,
           (SELECT a.id FROM accounts a
            WHERE a.tenant_id = ac.tenant_id
              AND a.organization_id IS NOT DISTINCT FROM ac.organization_id
              AND a.code = '6410' AND a.deleted_at IS NULL AND a.is_active = true
              AND COALESCE(a.is_leaf, true) = true
            LIMIT 1) AS acct_6410,
           row_number() OVER (ORDER BY ac.accrual_je_id) AS rn
    FROM accrued ac
    WHERE ac.acct_6710 IS NOT NULL
),
ins AS (
    INSERT INTO journal_entries (
        id, tenant_id, organization_id, journal_id, entry_number, entry_date,
        reference, description, source_type, source_id, exchange_rate,
        total_debit, total_credit, status, created_by, created_at, updated_at)
    SELECT uuid_generate_v4(), t.tenant_id, t.organization_id, t.journal_id,
           'PAYFIXT' || lpad((t.rn + (SELECT COUNT(*) FROM journal_entries x
                                      WHERE x.entry_number LIKE 'PAYFIXT%'))::text, 5, '0'),
           CURRENT_DATE,
           'legacy_tax_split',
           'Repair: withheld tax split (accrual credited 6710 at gross)',
           'payroll_repair', t.period_id, 1.0,
           t.legacy_tax, t.legacy_tax, 'posted', t.created_by, NOW(), NOW()
    FROM taxed t
    WHERE t.legacy_tax > 0 AND t.acct_6410 IS NOT NULL
    RETURNING id, source_id
),
line_dr AS (
    INSERT INTO journal_entry_lines (
        id, journal_entry_id, line_number, account_id, description,
        debit_amount, credit_amount, exchange_rate, created_at)
    SELECT uuid_generate_v4(), i.id, 1, t.acct_6710,
           'Wages Payable (tax split repair)', t.legacy_tax, 0, 1.0, NOW()
    FROM ins i JOIN taxed t ON t.period_id = i.source_id
    RETURNING id
)
INSERT INTO journal_entry_lines (
    id, journal_entry_id, line_number, account_id, description,
    debit_amount, credit_amount, exchange_rate, created_at)
SELECT uuid_generate_v4(), i.id, 2, t.acct_6410,
       'Tax liability (repair)', 0, t.legacy_tax, 1.0, NOW()
FROM ins i JOIN taxed t ON t.period_id = i.source_id;

UPDATE accounts a SET current_balance = current_balance + s.delta, updated_at = NOW()
FROM (
    SELECT l.account_id, SUM(l.debit_amount - l.credit_amount) AS delta
    FROM journal_entry_lines l
    JOIN journal_entries je ON je.id = l.journal_entry_id
    WHERE je.source_type = 'payroll_repair' AND je.reference = 'legacy_tax_split'
      AND je.created_at > NOW() - interval '1 minute'
    GROUP BY l.account_id
) s
WHERE a.id = s.account_id;

COMMIT;
