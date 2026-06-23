-- +migrate Up
-- The supplier/customer forms send a single "contact person" name (the person to
-- talk to at the company). Contacts had no place to store it — the normalized
-- contact_persons table is unused by these forms — so the value was silently
-- dropped on save and disappeared on refresh. Add a flat column for it.
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS contact_person VARCHAR(255);
