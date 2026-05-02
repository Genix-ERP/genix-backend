-- 382_products_name_to_text.sql
--
-- Widens `products.name` from `VARCHAR(255)` to `TEXT` so the
-- estimate-import auto-product creation no longer drops rows whose
-- name overflows the 255-char limit.
--
-- Why
-- ───
-- Construction estimate imports (especially electrical / engineering
-- system files) regularly include resource names like:
--
--   "PV-BOX НА 11 ОТХОДЯЩИХ ГРУПП, УКОМПЛЕКТОВАННЫЙ В СОСТАВЕ:
--    АВТОМАТИЧЕСКИЙ ВЫКЛЮЧАТЕЛЬ ПОСТОЯННОГО ТОКА 3Р-16А. CNCSGK
--    CSB8-125. 2ШТ, ОГРАНИЧИТЕЛЬ ИМПУЛЬСНЫХ ПЕРЕНАПРЯЖЕНИЙ
--    УЗИП 3,5КВ CNCSGK 3.6KV. 2ШТ, ..."
--
-- These are 300–500+ character strings. The auto-create flow inserts
-- them verbatim into `products.name` and Postgres rejects them with:
--
--   pq: value too long for type character varying(255)
--
-- Logs from production:
--   ERROR Failed to auto-create product from estimate
--     error: pq: value too long for type character varying(255)
--     name:  "PV-BOX НА 11 ОТХОДЯЩИХ ГРУПП..."
--
-- Each rejected name = one inventory product silently missed.
-- Switching to TEXT (no length limit in Postgres) lets the full name
-- land. No call site cares about the column having a fixed width —
-- name is read out as a string everywhere — so this is a drop-in
-- widening with zero downstream impact.
--
-- Performance is unchanged: in Postgres VARCHAR(N) and TEXT have
-- identical storage and index behaviour; the (N) check is purely a
-- length validation that runs on every insert/update. Removing it
-- also slightly speeds up bulk imports.
--
-- Idempotent: ALTER TYPE on an already-TEXT column is a no-op error,
-- but we use IF NOT EXISTS-style guard via a DO block so re-running
-- this migration on a DB that's already converted is safe.

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_name = 'products'
          AND column_name = 'name'
          AND data_type = 'character varying'
    ) THEN
        ALTER TABLE products ALTER COLUMN name TYPE TEXT;
    END IF;
END $$;

-- Same drop-in widening for `short_description` (VARCHAR(500)) — same
-- problem on long technical specs, same zero-impact fix.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_name = 'products'
          AND column_name = 'short_description'
          AND data_type = 'character varying'
    ) THEN
        ALTER TABLE products ALTER COLUMN short_description TYPE TEXT;
    END IF;
END $$;
