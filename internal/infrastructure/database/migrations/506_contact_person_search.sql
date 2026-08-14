-- 506: let a contact be found by the person who works there, not just by the
-- company name.
--
-- Two things were in the way.
--
-- 1. The supplier form has had a "Kontakt shaxs" input since it was written,
--    and it never saved anything. The front end posts `contact_person` to
--    POST/PUT /contacts, but CreateContactInput has no such field, so Gin
--    dropped it on the floor. Whatever the buyer typed vanished on reload.
--    Customers do not have this problem: CustomersContext writes its
--    "Kontakt shaxs" into contacts.legal_name and reads it back from there,
--    so that data is real and must keep working.
--
--    Rather than move the customers' data (legal_name is what the customer
--    card displays today, and rewriting live rows to fix a label is not worth
--    the risk), this adds the column the supplier side always assumed it had.
--    Search below covers both, so a person is findable whichever field their
--    company happened to use.
--
-- 2. Nothing was indexed for it. The picker searches on every keystroke, so
--    the person columns get the same trigram treatment contacts.name already
--    has from migration 002 (pg_trgm is enabled there).

ALTER TABLE contacts ADD COLUMN IF NOT EXISTS contact_person VARCHAR(255);

-- ILIKE '%term%' can only use an index with gin_trgm_ops; a btree is useless
-- for an unanchored match.
CREATE INDEX IF NOT EXISTS idx_contacts_contact_person_trgm
    ON contacts USING gin (contact_person gin_trgm_ops);

CREATE INDEX IF NOT EXISTS idx_contacts_legal_name_trgm
    ON contacts USING gin (legal_name gin_trgm_ops);

-- The picker matches a typed "Alisher Karimov" against the joined name, so
-- index the expression the query actually uses rather than the two columns
-- separately. COALESCE keeps the expression non-null; both columns are NOT
-- NULL today but the index outlives that guarantee.
CREATE INDEX IF NOT EXISTS idx_contact_persons_fullname_trgm
    ON contact_persons USING gin (
        (COALESCE(first_name, '') || ' ' || COALESCE(last_name, '')) gin_trgm_ops
    );
