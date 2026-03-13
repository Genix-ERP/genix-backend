-- Widen item_number and parent_item_number to handle longer values from imported spreadsheets
ALTER TABLE construction_estimate_line
  ALTER COLUMN item_number TYPE VARCHAR(255),
  ALTER COLUMN parent_item_number TYPE VARCHAR(255);
