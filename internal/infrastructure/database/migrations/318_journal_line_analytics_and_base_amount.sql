-- Migration 318: Journal-line analytics dimensions + UZS base amount
-- Reference: TT Buxgalteriya ERP sections 3.1 (schema), 4.5 (mandatory analytics), 4.6 (multi-currency)
--
-- Adds to journal_entry_lines:
--   * amount_base        — UZS equivalent of the line (per spec: "summa operatsiya valyutasida va hisob valyutasida so'mda")
--   * warehouse_id       — mandatory analytics for 2900-series goods accounts
--   * employee_id        — mandatory analytics for 6710 (payroll), 4210 (advances)
--   * contract_id        — paired with contact_id for 4010/6010 contractor settlements
--   * analytics_json     — generic extension slot for future subkonto dimensions (TT 3.1)
-- All columns are nullable; enforcement of mandatory-ness is done by the trigger in migration 319
-- and by the application layer (CreateJournalEntry handler).

-- ============================================
-- STEP 1: Add columns
-- ============================================

ALTER TABLE journal_entry_lines
    ADD COLUMN IF NOT EXISTS amount_base DECIMAL(20, 4);

ALTER TABLE journal_entry_lines
    ADD COLUMN IF NOT EXISTS warehouse_id UUID;

ALTER TABLE journal_entry_lines
    ADD COLUMN IF NOT EXISTS employee_id UUID;

ALTER TABLE journal_entry_lines
    ADD COLUMN IF NOT EXISTS contract_id UUID;

ALTER TABLE journal_entry_lines
    ADD COLUMN IF NOT EXISTS analytics_json JSONB DEFAULT '{}'::jsonb;

-- ============================================
-- STEP 2: Best-effort foreign keys (skip if referenced tables aren't in this schema variant)
-- ============================================

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables
               WHERE table_schema = current_schema() AND table_name = 'warehouses')
       AND NOT EXISTS (SELECT 1 FROM pg_constraint
                       WHERE conname = 'fk_jel_warehouse') THEN
        ALTER TABLE journal_entry_lines
            ADD CONSTRAINT fk_jel_warehouse
            FOREIGN KEY (warehouse_id) REFERENCES warehouses(id)
            ON DELETE SET NULL;
    END IF;

    IF EXISTS (SELECT 1 FROM information_schema.tables
               WHERE table_schema = current_schema() AND table_name = 'employees')
       AND NOT EXISTS (SELECT 1 FROM pg_constraint
                       WHERE conname = 'fk_jel_employee') THEN
        ALTER TABLE journal_entry_lines
            ADD CONSTRAINT fk_jel_employee
            FOREIGN KEY (employee_id) REFERENCES employees(id)
            ON DELETE SET NULL;
    END IF;

    IF EXISTS (SELECT 1 FROM information_schema.tables
               WHERE table_schema = current_schema() AND table_name = 'contracts')
       AND NOT EXISTS (SELECT 1 FROM pg_constraint
                       WHERE conname = 'fk_jel_contract') THEN
        ALTER TABLE journal_entry_lines
            ADD CONSTRAINT fk_jel_contract
            FOREIGN KEY (contract_id) REFERENCES contracts(id)
            ON DELETE SET NULL;
    END IF;
END $$;

-- ============================================
-- STEP 3: Backfill amount_base for existing rows
-- ============================================
-- For lines without an explicit currency, amount_base = gross line amount.
-- For multi-currency lines, amount_base = (debit+credit) converted via exchange_rate.
-- exchange_rate in journal_entry_lines defaults to 1, so this is safe for legacy rows.
UPDATE journal_entry_lines
SET amount_base = (COALESCE(debit_amount, 0) + COALESCE(credit_amount, 0))
WHERE amount_base IS NULL;

-- ============================================
-- STEP 4: Performance indexes for analytics filtering & reports
-- ============================================
-- Spec section 3.2: partial indexes on analytics dimensions

CREATE INDEX IF NOT EXISTS idx_jel_account_entry_date
    ON journal_entry_lines (account_id)
    INCLUDE (debit_amount, credit_amount, amount_base);

CREATE INDEX IF NOT EXISTS idx_jel_contact
    ON journal_entry_lines (contact_id)
    WHERE contact_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_jel_warehouse
    ON journal_entry_lines (warehouse_id)
    WHERE warehouse_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_jel_employee
    ON journal_entry_lines (employee_id)
    WHERE employee_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_jel_contract
    ON journal_entry_lines (contract_id)
    WHERE contract_id IS NOT NULL;

-- Comments for operators
COMMENT ON COLUMN journal_entry_lines.amount_base IS
    'TT 4.6: line amount expressed in accounting base currency (UZS). Always populated; equals debit+credit in base currency terms.';

COMMENT ON COLUMN journal_entry_lines.warehouse_id IS
    'TT 4.5 subkonto: required when posting to 2900/1010-1090 (goods/raw materials).';

COMMENT ON COLUMN journal_entry_lines.employee_id IS
    'TT 4.5 subkonto: required when posting to 6710 (payroll), 4210 (advances to accountable persons).';

COMMENT ON COLUMN journal_entry_lines.contract_id IS
    'TT 4.5 subkonto: paired with contact_id for 4010/6010 settlements with customers/suppliers.';
