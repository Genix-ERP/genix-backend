-- 473_daily_report_quantity.sql — qurilish-v2 portfel-turkumi.
--
-- «Kunlik jurnal» web-modali construction_daily_reports'ga yozadi (mobil
-- daily_log'dan alohida parallel jadval). Bajarilgan hajm maydonlari unda
-- yo'q edi — UI endi yuboradi, backend qabul qilib saqlaydi. KS-2 avto-gen
-- hozircha construction_daily_log.wbs_id bo'yicha yig'adi — reports↔KS-2
-- ko'prigi follow-up bo'lib qoladi (docs/qurilish-v2/audit.md §4).
ALTER TABLE construction_daily_reports ADD COLUMN IF NOT EXISTS quantity_done NUMERIC(18,4) NOT NULL DEFAULT 0;
ALTER TABLE construction_daily_reports ADD COLUMN IF NOT EXISTS uom VARCHAR(50);
