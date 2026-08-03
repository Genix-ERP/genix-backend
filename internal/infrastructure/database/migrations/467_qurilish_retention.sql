-- 467_qurilish_retention.sql
-- S5: retention (kafolat ushlab qolish) sub tomonda — docs/construction-roadmap.md.
--
-- construction_subcontract.retention_pct already exists (223-era). This adds
-- the per-act realised figure: when a sub act posts (S4, migration 466) and
-- the subcontract carries retention_pct > 0, the credit side splits:
--   Cr 6010                 = amount − retention   (to'lanadigan qism)
--   Cr retention account    = retention            (kafolat muddatigacha)
-- The retention account comes from construction_account_mapping key
-- 'retention_6990' (per-tenant, configurable); the seeded fallback is 6990
-- "Boshqa kreditorlik" so current payables on 6010 stay clean. If the tenant's
-- chart can't resolve a DISTINCT retention account, the full amount stays on
-- 6010 and retention_amount is still recorded for reporting.
-- NOTE (BHMS): account choice is per-tenant data; verify against the tenant's
-- accounting policy — 6990 is a default, not a mandate.
ALTER TABLE construction_act
    ADD COLUMN IF NOT EXISTS retention_amount NUMERIC(18,2) NOT NULL DEFAULT 0;
