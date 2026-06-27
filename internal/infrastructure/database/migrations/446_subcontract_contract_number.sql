-- 446_subcontract_contract_number.sql
--
-- Contract number for a subcontractor. Entered by the user when creating/editing
-- the subcontractor and printed on the Forma 3 (КС-3) "Договор" line, alongside
-- the contract amount (construction_subcontract.amount) and date (start_date).

ALTER TABLE construction_subcontract
    ADD COLUMN IF NOT EXISTS contract_number VARCHAR(100);
