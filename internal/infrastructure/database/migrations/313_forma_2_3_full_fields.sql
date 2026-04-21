-- =====================================================
-- Migration: 313_forma_2_3_full_fields.sql
-- Purpose: Add all fields required to render Forma 2 (KS-2)
--          and Forma 3 (KS-3) documents matching the
--          reference industry format (сиеб булдингс / форма 3).
-- =====================================================

-- ---------------------------------------------------------------
-- CONSTRUCTION_PROJECTS — Client (Заказчик) identity block
-- Used by Forma 3 header.
-- ---------------------------------------------------------------
ALTER TABLE construction_projects ADD COLUMN IF NOT EXISTS client_address TEXT;
ALTER TABLE construction_projects ADD COLUMN IF NOT EXISTS client_bank_name VARCHAR(255);
ALTER TABLE construction_projects ADD COLUMN IF NOT EXISTS client_bank_account VARCHAR(50);
ALTER TABLE construction_projects ADD COLUMN IF NOT EXISTS client_mfo VARCHAR(20);
ALTER TABLE construction_projects ADD COLUMN IF NOT EXISTS client_stir VARCHAR(20);
ALTER TABLE construction_projects ADD COLUMN IF NOT EXISTS client_okonh VARCHAR(20);
ALTER TABLE construction_projects ADD COLUMN IF NOT EXISTS contract_number VARCHAR(100);
ALTER TABLE construction_projects ADD COLUMN IF NOT EXISTS object_full_name TEXT;

-- ---------------------------------------------------------------
-- CONSTRUCTION_SUBCONTRACT — Contractor (Подрядчик) identity block
-- Used by Forma 3 header.
-- ---------------------------------------------------------------
ALTER TABLE construction_subcontract ADD COLUMN IF NOT EXISTS address TEXT;
ALTER TABLE construction_subcontract ADD COLUMN IF NOT EXISTS phone VARCHAR(50);
ALTER TABLE construction_subcontract ADD COLUMN IF NOT EXISTS bank_name VARCHAR(255);
ALTER TABLE construction_subcontract ADD COLUMN IF NOT EXISTS bank_account VARCHAR(50);
ALTER TABLE construction_subcontract ADD COLUMN IF NOT EXISTS mfo VARCHAR(20);
ALTER TABLE construction_subcontract ADD COLUMN IF NOT EXISTS stir VARCHAR(20);
ALTER TABLE construction_subcontract ADD COLUMN IF NOT EXISTS okonh VARCHAR(20);
ALTER TABLE construction_subcontract ADD COLUMN IF NOT EXISTS director_name VARCHAR(255);
ALTER TABLE construction_subcontract ADD COLUMN IF NOT EXISTS chief_accountant_name VARCHAR(255);

-- Also add client (Заказчик) signatory names on the project
ALTER TABLE construction_projects ADD COLUMN IF NOT EXISTS client_director_name VARCHAR(255);
ALTER TABLE construction_projects ADD COLUMN IF NOT EXISTS client_chief_accountant_name VARCHAR(255);
ALTER TABLE construction_projects ADD COLUMN IF NOT EXISTS client_phone VARCHAR(50); -- may already exist; IF NOT EXISTS makes this safe

-- ---------------------------------------------------------------
-- CONSTRUCTION_ACT — Forma 2 totals breakdown & Forma 3 coefficients
-- ---------------------------------------------------------------
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS f2_labor_total     DECIMAL(18,2) DEFAULT 0; -- ЗАРПЛАТА
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS f2_equipment_total DECIMAL(18,2) DEFAULT 0; -- ЭММ
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS f2_materials_total DECIMAL(18,2) DEFAULT 0; -- МАТЕРИАЛЫ
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS f2_cables_total    DECIMAL(18,2) DEFAULT 0; -- КАБЕЛИ

-- Configurable coefficients (percentages)
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS f2_transport_pct DECIMAL(5,2) DEFAULT 5;   -- ТРАНСПОРТ МАТЕРИАЛОВ 5%
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS f2_other_pct     DECIMAL(5,2) DEFAULT 17;  -- ПРОЧИЕ ЗАТРАТЫ 17%

-- Computed snapshots of the totals rows (for audit / frozen printing)
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS f2_transport_amount    DECIMAL(18,2) DEFAULT 0;
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS f2_other_amount_calc   DECIMAL(18,2) DEFAULT 0;
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS f2_materials_returned  DECIMAL(18,2) DEFAULT 0; -- ВОЗВРАТ МАТЕРИАЛОВ ЗАКАЗЧИКА
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS f2_contractor_total    DECIMAL(18,2) DEFAULT 0; -- ИТОГО ВЫПОЛНЕНИЕ ПОДРЯДЧИКА

-- Reporting period as calendar-months (needed for Forma 3: "с январь по февраль")
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS period_month_from SMALLINT;
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS period_month_to   SMALLINT;
ALTER TABLE construction_act ADD COLUMN IF NOT EXISTS period_year       INT;

-- ---------------------------------------------------------------
-- CONSTRUCTION_ACT_LINE — Forma 2 row hierarchy & cost split
-- ---------------------------------------------------------------
ALTER TABLE construction_act_line ADD COLUMN IF NOT EXISTS parent_line_id BIGINT REFERENCES construction_act_line(id) ON DELETE CASCADE;
ALTER TABLE construction_act_line ADD COLUMN IF NOT EXISTS line_number_display VARCHAR(20); -- "1", "1.1", "1.2", "2", ...
ALTER TABLE construction_act_line ADD COLUMN IF NOT EXISTS is_section_header BOOLEAN DEFAULT FALSE; -- РАЗДЕЛ 1.ЛЭП-10 КВ (В/В)
ALTER TABLE construction_act_line ADD COLUMN IF NOT EXISTS section_name TEXT;
ALTER TABLE construction_act_line ADD COLUMN IF NOT EXISTS norm_code VARCHAR(50); -- "Е3304-003-01", "1070", "31226"

-- Forma 2 cost split on a per-line basis
ALTER TABLE construction_act_line ADD COLUMN IF NOT EXISTS labor_amount     DECIMAL(18,2) DEFAULT 0; -- ЗАРПЛАТА
ALTER TABLE construction_act_line ADD COLUMN IF NOT EXISTS equipment_amount DECIMAL(18,2) DEFAULT 0; -- ЭММ
ALTER TABLE construction_act_line ADD COLUMN IF NOT EXISTS materials_amount DECIMAL(18,2) DEFAULT 0; -- МАТЕРИАЛЫ
ALTER TABLE construction_act_line ADD COLUMN IF NOT EXISTS cables_amount    DECIMAL(18,2) DEFAULT 0; -- КАБЕЛИ

-- Plan vs period quantity is already covered by qty_smeta + quantity.
-- Add qty_period as a synonym for clarity; many handlers already use `quantity` as "period qty".
-- No rename; just a comment for documentation.

-- ---------------------------------------------------------------
-- Helpful indexes for hierarchical line rendering
-- ---------------------------------------------------------------
CREATE INDEX IF NOT EXISTS idx_act_line_parent ON construction_act_line(parent_line_id);
CREATE INDEX IF NOT EXISTS idx_act_line_section ON construction_act_line(act_id, is_section_header);
