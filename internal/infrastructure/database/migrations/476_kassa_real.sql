-- 476_kassa_real.sql — Moliya v2: Kassa (PKO/RKO) haqiqiy bo'ladi.
--
-- /cash/orders, /cash/registers, /cash/book stub edi (finance_extra.go);
-- hujjatlar endi cash_orders'da saqlanadi va confirm'da ledger'ga
-- balansli JE bilan o'tadi. Jadval schema'si deyarli tayyor, ikki teshik:
--
-- 1. cash_registers'da GL bog'lanish yo'q edi — yagona pul dvigateli
--    (posted ledger) ishlashi uchun har bir kassa 50xx leaf schyotiga
--    (odatda 5010) ulanadi. NULL bo'lsa handler 5010'ga fallback qiladi.
-- 2. cash_orders'da kontragent faqat partner_id (contacts FK) edi —
--    PKO/RKO blankasida kontakt bo'lmagan shaxs (kuryer, hodim) uchun
--    erkin matnli counterparty_name kerak.
ALTER TABLE cash_registers ADD COLUMN IF NOT EXISTS account_id UUID REFERENCES accounts(id) ON DELETE SET NULL;
ALTER TABLE cash_orders ADD COLUMN IF NOT EXISTS counterparty_name VARCHAR(255);
CREATE INDEX IF NOT EXISTS idx_cash_orders_status ON cash_orders(tenant_id, status);
