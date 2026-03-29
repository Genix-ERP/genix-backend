-- Add translation columns to journals
ALTER TABLE journals ADD COLUMN IF NOT EXISTS name_uz VARCHAR(100);
ALTER TABLE journals ADD COLUMN IF NOT EXISTS name_en VARCHAR(100);

-- Populate translations for default journals
-- name = Russian, name_uz = Uzbek, name_en = English

UPDATE journals SET name = 'Банковский журнал', name_uz = 'Bank jurnali', name_en = 'Bank Journal' WHERE code = 'BANK' AND name_uz IS NULL;
UPDATE journals SET name = 'Банк', name_uz = 'Bank', name_en = 'Bank' WHERE code = 'BANK_1' AND name_uz IS NULL;
UPDATE journals SET name = 'Кассовый журнал', name_uz = 'Kassa jurnali', name_en = 'Cash Journal' WHERE code = 'CASH' AND name_uz IS NULL;
UPDATE journals SET name = 'Журнал кассовых поступлений', name_uz = 'Naqd pul tushumlari jurnali', name_en = 'Cash Receipts Journal' WHERE code = 'CASH_RECEIPTS' AND name_uz IS NULL;
UPDATE journals SET name = 'Главный журнал', name_uz = 'Bosh jurnal', name_en = 'General Journal' WHERE code = 'GEN' AND name_uz IS NULL;
UPDATE journals SET name = 'Главный журнал', name_uz = 'Bosh jurnal', name_en = 'General Journal' WHERE code = 'GENERAL' AND name_uz IS NULL;
UPDATE journals SET name = 'Прочие операции', name_uz = 'Boshqa operatsiyalar jurnali', name_en = 'Miscellaneous Journal' WHERE code = 'MISC' AND name_uz IS NULL;
UPDATE journals SET name = 'Журнал зарплаты', name_uz = 'Ish haqi jurnali', name_en = 'Payroll Journal' WHERE code = 'PAYROLL' AND name_uz IS NULL;
UPDATE journals SET name = 'Журнал закупок', name_uz = 'Xarid jurnali', name_en = 'Purchase Journal' WHERE code = 'PUR' AND name_uz IS NULL;
UPDATE journals SET name = 'Журнал закупок', name_uz = 'Xarid jurnali', name_en = 'Purchase Journal' WHERE code = 'PURCH' AND name_uz IS NULL;
UPDATE journals SET name = 'Журнал закупок', name_uz = 'Xarid jurnali', name_en = 'Purchase Journal' WHERE code = 'PURCHASE' AND name_uz IS NULL;
UPDATE journals SET name = 'Журнал продаж', name_uz = 'Sotish jurnali', name_en = 'Sales Journal' WHERE code = 'SAL' AND name_uz IS NULL;
UPDATE journals SET name = 'Журнал продаж', name_uz = 'Sotish jurnali', name_en = 'Sales Journal' WHERE code = 'SALES' AND name_uz IS NULL;
UPDATE journals SET name = 'Складской журнал', name_uz = 'Ombor jurnali', name_en = 'Stock Journal' WHERE code = 'STOCK' AND name_uz IS NULL;
UPDATE journals SET name = 'Журнал основных средств', name_uz = 'Asosiy vositalar jurnali', name_en = 'Fixed Assets Journal' WHERE code = 'ASSET' AND name_uz IS NULL;
UPDATE journals SET name = 'Строительный журнал', name_uz = 'Qurilish jurnali', name_en = 'Construction Journal' WHERE code = 'CONST' AND name_uz IS NULL;
