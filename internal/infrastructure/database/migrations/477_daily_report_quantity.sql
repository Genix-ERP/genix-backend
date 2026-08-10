-- 473_daily_report_quantity.sql — qurilish-v2 portfel-turkumi.
--
-- RENUMBERED 473 -> 477. The original number collided with a migration already
-- on main, and schema_migrations.version is a PRIMARY KEY: two files sharing a
-- version both land in `pending`, both apply, and the second INSERT violates
-- the key — so RunMigrations returns an error and the API crash-loops on boot.
-- On a database that had already recorded that version, the loser is instead
-- skipped forever and its columns silently never appear. Every statement here
-- is ADD COLUMN IF NOT EXISTS, so running under the new number is a no-op
-- wherever it somehow already ran.
--
-- «Kunlik jurnal» web-modali construction_daily_reports'ga yozadi (mobil
-- daily_log'dan alohida parallel jadval). Bajarilgan hajm maydonlari unda
-- yo'q edi — UI endi yuboradi, backend qabul qilib saqlaydi. KS-2 avto-gen
-- hozircha construction_daily_log.wbs_id bo'yicha yig'adi — reports↔KS-2
-- ko'prigi follow-up bo'lib qoladi (docs/qurilish-v2/audit.md §4).
ALTER TABLE construction_daily_reports ADD COLUMN IF NOT EXISTS quantity_done NUMERIC(18,4) NOT NULL DEFAULT 0;
ALTER TABLE construction_daily_reports ADD COLUMN IF NOT EXISTS uom VARCHAR(50);
