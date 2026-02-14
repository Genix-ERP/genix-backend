-- Make end_date nullable as it's optional in the handler

ALTER TABLE procurement_contracts ALTER COLUMN end_date DROP NOT NULL;
