-- 477_budget_exceeded_marker.sql — Moliya v2 (Byudjetlar).
--
-- finance.budget_exceeded hodisasi "bir kesishishga bir marta" yonishi uchun
-- marker: chiziq fakti (posted JEL dan hisoblangan) rejadan oshganda
-- exceeded_notified_at qo'yiladi va hodisa emit qilinadi; fakt yana reja
-- ostiga tushsa marker tozalanadi (qayta qurollanadi). Tekshiruv nuqtalari:
-- ListBudgetLines va GetBudgetPlanVsActual (finance.go, checkBudgetLineExceeded).
ALTER TABLE budget_lines ADD COLUMN IF NOT EXISTS exceeded_notified_at TIMESTAMPTZ;
