-- 403_backfill_journal_localized_names_and_retire_cash_receipts.sql
--
-- Two-part cleanup for the default journals:
--
-- 1. Backfill name_uz / name_en columns on EXISTING default journals.
--    Migration 278 wrote the localized columns for journals it
--    created, but journals seeded by the older runtime path (or
--    journals created by tenants before migration 278 ran) have
--    NULL name_uz / NULL name_en. The frontend currently falls back
--    to a hardcoded code → label map, but the cleaner answer is to
--    fill the DB columns so search, exports, and reports also use
--    the localized names.
--
--    We match on the immutable `code` column so this is safe even
--    if a user manually renamed the journal (we only fill the
--    localized columns if they're currently NULL or empty — never
--    overwrite a user's customization).
--
-- 2. Retire the CASH_RECEIPTS default journal. It was redundant with
--    CASH (both type='cash', both default account = 5010 Kassa).
--    The handler code in sales_returns.go already falls back to
--    'CASH' when 'CASH_RECEIPTS' isn't found, so removing
--    CASH_RECEIPTS doesn't break any downstream flow.
--
--    Soft-delete (deleted_at = NOW()) rather than DELETE so journal
--    entries that historically referenced CASH_RECEIPTS keep their
--    FK references valid. New entries won't pick this journal
--    because the list view filters `deleted_at IS NULL`.
--
-- Idempotent: re-running the migration finds zero rows to update on
-- the second pass.

-- Step 1: Backfill localized names for the 10 keeper journals.
-- Column references on the SET side must use the bare column name
-- (no table prefix — PostgreSQL syntax), but the CASE/WHERE side
-- must qualify with the table name because the VALUES alias also
-- has `name_uz` / `name_en` columns. Aliasing the VALUES columns
-- to `uz_name` / `en_name` removes the ambiguity entirely.
UPDATE journals SET
    name_uz = CASE
        WHEN COALESCE(journals.name_uz, '') = '' THEN v.uz_name
        ELSE journals.name_uz
    END,
    name_en = CASE
        WHEN COALESCE(journals.name_en, '') = '' THEN v.en_name
        ELSE journals.name_en
    END,
    updated_at = NOW()
FROM (VALUES
    ('GEN',     'Bosh jurnal',                  'General Journal'),
    ('SAL',     'Sotish jurnali',               'Sales Journal'),
    ('PUR',     'Xarid jurnali',                'Purchase Journal'),
    ('CASH',    'Kassa jurnali',                'Cash Journal'),
    ('BANK',    'Bank jurnali',                 'Bank Journal'),
    ('MISC',    'Boshqa operatsiyalar jurnali', 'Miscellaneous Journal'),
    ('STOCK',   'Ombor jurnali',                'Stock Journal'),
    ('ASSET',   'Asosiy vositalar jurnali',     'Fixed Assets Journal'),
    ('PAYROLL', 'Ish haqi jurnali',             'Payroll Journal'),
    ('CONST',   'Qurilish jurnali',             'Construction Journal')
) AS v(code, uz_name, en_name)
WHERE journals.code = v.code
  AND journals.deleted_at IS NULL
  AND (COALESCE(journals.name_uz, '') = '' OR COALESCE(journals.name_en, '') = '');

-- Step 2: Soft-delete the legacy CASH_RECEIPTS journal across all
-- tenants / organizations. Journal entries that referenced it keep
-- the FK link (the row is still present, just marked deleted) so
-- nothing breaks historically.
UPDATE journals
SET deleted_at = NOW(),
    is_active = false,
    updated_at = NOW()
WHERE code = 'CASH_RECEIPTS'
  AND deleted_at IS NULL;
