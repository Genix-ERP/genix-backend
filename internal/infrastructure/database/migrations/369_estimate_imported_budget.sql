-- Migration 369: Imported budget total per estimate
--
-- The user's complaint was that the Reja vs Fakt page's top "Reja jami"
-- card was computed by SUMming each work's plan_qty × effective_rate —
-- that always under- or over-shoots the real project budget because it
-- ignores transport overhead, indirect costs, etc.
--
-- The Ресурс Excel sheet ALREADY carries the canonical totals at the
-- bottom of the sheet:
--
--   ИТОГО ПО СТРОИТЕЛЬНЫМ МАТЕРИАЛАМ   (e.g. 7,848,662,755 СУМ)
--   ТРАНСПОРТНЫЕ РАСХОДЫ НА МАТЕРИАЛЫ  (transport overhead)
--   ИТОГО ПРЯМЫЕ ЗАТРАТЫ                (e.g. 11,185,600,000 СУМ)
--
-- We capture these during import and persist them on the estimate so the
-- Reja vs Fakt summary can display the real project budget verbatim.
--
-- material_budget    = ИТОГО ПО СТРОИТЕЛЬНЫМ МАТЕРИАЛАМ
-- transport_budget   = ТРАНСПОРТНЫЕ РАСХОДЫ НА МАТЕРИАЛЫ
-- budget_total       = ИТОГО ПРЯМЫЕ ЗАТРАТЫ (the canonical project budget)
--
-- All three default to 0 so existing rows are unaffected.

ALTER TABLE construction_estimate
    ADD COLUMN IF NOT EXISTS budget_total       DECIMAL(18,2) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS material_budget    DECIMAL(18,2) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS transport_budget   DECIMAL(18,2) NOT NULL DEFAULT 0;
