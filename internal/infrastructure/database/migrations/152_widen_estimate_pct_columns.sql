-- Widen percentage columns to DECIMAL(7,2) to handle values up to 99999.99
ALTER TABLE construction_estimate ALTER COLUMN overhead_pct TYPE DECIMAL(7,2);
ALTER TABLE construction_estimate ALTER COLUMN profit_pct TYPE DECIMAL(7,2);
ALTER TABLE construction_estimate ALTER COLUMN vat_pct TYPE DECIMAL(7,2);
