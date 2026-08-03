-- 461_fix_6710_account_type.sql — normalize stale expense-typed 6710 rows (C4)
--
-- History of the conflict:
--   * 185_misc_journal_fixes.sql seeded 6710 per org as 'Salary Expense'
--     (name_uz 'Ish haqi xarajati') with account type OPEX — an EXPENSE.
--   * 315_nas_chart_of_accounts.sql defines 6710 as the NAS payable
--     "Mehnat haqi bo'yicha xodimlarga bo'lgan qarz" — a LIABILITY. But 315
--     only RENAMES: phase 1 moves a pre-existing 2110 (Wages Payable,
--     ST_LIAB per 081) to T6710, phase 2 renames T6710→6710 (or, when a
--     6710 already exists, renames THAT row and soft-deletes the temp).
--     Neither phase touches account_type_id, and STEP 2's insert is
--     ON CONFLICT DO NOTHING.
--   => Orgs that never had a 2110 kept the 185 row untouched: code 6710,
--      name 'Salary Expense', type OPEX. Payroll and the manufacturing
--      completion flow CREDIT 6710 and bump its balance as credit-normal —
--      so on these rows current_balance is sign-flipped and the salary
--      liability shows up inside operating expenses on the P&L.
--
-- Safety: no code path branches on 6710's account TYPE — findAccount
-- matches name/code + leafness only; all writers already treat 6710 as
-- credit-normal. Reports group by type, which is exactly what this fixes.
-- The balance is recomputed from posted GL lines under the corrected
-- normal_balance (akt-sverka source of truth), for every live 6710 row.

-- 1. Retype + rename the stale 185-seeded rows to the 315 definition.
UPDATE accounts a
SET account_type_id = (SELECT id FROM account_types WHERE code = 'ST_LIAB' LIMIT 1),
    name        = 'Mehnat haqi bo''yicha xodimlarga bo''lgan qarz',
    name_uz     = 'Mehnat haqi bo''yicha xodimlarga bo''lgan qarz',
    name_en     = 'Salaries Payable',
    description = 'Ish haqi va maoshlar to''lanishi kerak',
    updated_at  = NOW()
WHERE a.code = '6710'
  AND a.deleted_at IS NULL
  AND (a.name ILIKE '%salary expense%'
       OR a.name ILIKE '%ish haqi xarajati%'
       OR a.name_uz ILIKE '%ish haqi xarajati%')
  AND EXISTS (SELECT 1 FROM account_types WHERE code = 'ST_LIAB');

-- 2. Recompute every live 6710 balance from posted lines under the row's
--    (possibly just corrected) normal_balance. Idempotent.
UPDATE accounts a
SET current_balance = CASE
        WHEN at.normal_balance = 'debit' THEN s.dt - s.kt
        ELSE                                  s.kt - s.dt
    END,
    updated_at = NOW()
FROM account_types at,
     (SELECT acc.id AS account_id,
             COALESCE(SUM(jel.debit_amount)  FILTER (WHERE je.status = 'posted' AND je.deleted_at IS NULL), 0) AS dt,
             COALESCE(SUM(jel.credit_amount) FILTER (WHERE je.status = 'posted' AND je.deleted_at IS NULL), 0) AS kt
      FROM accounts acc
      LEFT JOIN journal_entry_lines jel ON jel.account_id = acc.id
      LEFT JOIN journal_entries je ON je.id = jel.journal_entry_id
      WHERE acc.code = '6710' AND acc.deleted_at IS NULL
      GROUP BY acc.id) s
WHERE a.account_type_id = at.id
  AND s.account_id = a.id
  AND a.code = '6710'
  AND a.deleted_at IS NULL;
