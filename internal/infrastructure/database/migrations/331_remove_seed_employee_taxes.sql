-- 331_remove_seed_employee_taxes.sql
-- Reverts the seeding block from migration 330. The product owner asked to stop
-- pre-creating NDFL / INPS / SOC_TAX rows per tenant — admins will define their
-- own employee taxes from Settings → Finance → Employee Taxes.
--
-- Deletion rules (safety):
--   Only remove rows that are clearly untouched seeds — same code, still
--   inactive, no account assigned, and never modified after creation. Anything
--   the admin already started configuring (toggled active, assigned an account,
--   edited the name/rate, etc.) is preserved.
--
--   Deletion here is permanent (DELETE, not soft-delete) because these rows
--   were never user-created in the first place and have no history worth keeping.

DELETE FROM employee_taxes
WHERE
    UPPER(code) IN ('NDFL', 'INPS', 'SOC_TAX')
    AND is_active = FALSE
    AND account_id IS NULL
    AND expense_account_id IS NULL
    AND created_by IS NULL
    AND updated_at = created_at
    AND deleted_at IS NULL
    -- Extra safety: skip rows that already have entries applied to them
    AND NOT EXISTS (
        SELECT 1 FROM payroll_entry_taxes pet
        WHERE pet.tax_id = employee_taxes.id
    );
