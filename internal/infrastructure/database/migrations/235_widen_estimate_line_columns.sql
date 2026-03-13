-- Widen remaining narrow columns on estimate lines for imported spreadsheet data
ALTER TABLE construction_estimate_line
  ALTER COLUMN uom TYPE VARCHAR(255),
  ALTER COLUMN code TYPE VARCHAR(255),
  ALTER COLUMN name TYPE TEXT;
