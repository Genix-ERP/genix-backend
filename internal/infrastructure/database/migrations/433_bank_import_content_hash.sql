-- 433_bank_import_content_hash.sql
--
-- Adds a content_hash column to bank_statement_imports so re-uploading the
-- same 1C bank-client file can be detected and rejected. Without it, importing
-- the same statement twice duplicated every transaction and doubled the
-- credit/debit totals (there was no file hash and no unique key).
--
-- The hash is the SHA-256 of the full uploaded file, computed in the handler.
-- We look it up per tenant before inserting; a match returns the existing
-- import instead of creating duplicates.

ALTER TABLE bank_statement_imports
    ADD COLUMN IF NOT EXISTS content_hash TEXT;

CREATE INDEX IF NOT EXISTS idx_bank_statement_imports_content_hash
    ON bank_statement_imports (tenant_id, content_hash)
    WHERE content_hash IS NOT NULL;
