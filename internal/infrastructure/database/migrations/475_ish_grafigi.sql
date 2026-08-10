-- 471_ish_grafigi.sql — Ish grafigi (Gantt) v2, S8 birinchi bosqich.
--
-- RENUMBERED 471 -> 475. The original number collided with a migration already
-- on main, and schema_migrations.version is a PRIMARY KEY: two files sharing a
-- version both land in `pending`, both apply, and the second INSERT violates
-- the key — so RunMigrations returns an error and the API crash-loops on boot.
-- On a database that had already recorded that version, the loser is instead
-- skipped forever and its columns silently never appear. Every statement here
-- is ADD COLUMN IF NOT EXISTS, so running under the new number is a no-op
-- wherever it somehow already ran.
--
-- Gectaro modeli: smeta va grafik BITTA ish ro'yxatining ikki ko'rinishi.
-- Shuning uchun sana maydonlari to'g'ridan-to'g'ri ish qatorlariga
-- (construction_estimate_line, ish = resource_type='' AND parent_line_id=0)
-- qo'shiladi — alohida schedule jadvali YO'Q, divergensiya bo'lmaydi.
-- Davomiylik saqlanmaydi (end - start + 1 dan hisoblanadi); progress ham
-- saqlanmaydi (done_quantity fakt-daftaridan hisoblanadi — 4-chi progress
-- tushunchasi yaratilmaydi, docs/qurilish-audit.md §3).

ALTER TABLE construction_estimate_line ADD COLUMN IF NOT EXISTS sched_start DATE;
ALTER TABLE construction_estimate_line ADD COLUMN IF NOT EXISTS sched_end DATE;
-- «Grafikni muzlatish» nusxasi — ghost-barlar va reja-fakt vaqt tahlili uchun.
ALTER TABLE construction_estimate_line ADD COLUMN IF NOT EXISTS baseline_start DATE;
ALTER TABLE construction_estimate_line ADD COLUMN IF NOT EXISTS baseline_end DATE;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'chk_estimate_line_sched_range'
    ) THEN
        ALTER TABLE construction_estimate_line
            ADD CONSTRAINT chk_estimate_line_sched_range
            CHECK (sched_start IS NULL OR sched_end IS NULL OR sched_end >= sched_start);
    END IF;
END $$;

-- work_overdue skaneri uchun: tenant bo'yicha muddati o'tgan ishlarni tez topish.
CREATE INDEX IF NOT EXISTS idx_estimate_line_sched_end
    ON construction_estimate_line (tenant_id, sched_end)
    WHERE sched_end IS NOT NULL;

-- FS (finish-to-start) bog'liqliklar, faqat v1: lag_days bilan.
-- predecessor/successor — ish qatorlari (estimate_line.id, BIGINT).
-- Sikl tekshiruvi yozish paytida Go'da (BFS); jadval darajasida faqat
-- o'z-o'ziga bog'lanish va dublikat taqiqlanadi.
CREATE TABLE IF NOT EXISTS construction_work_dependencies (
    id                  BIGSERIAL PRIMARY KEY,
    tenant_id           UUID NOT NULL REFERENCES tenants(id),
    project_id          BIGINT NOT NULL REFERENCES construction_projects(id) ON DELETE CASCADE,
    predecessor_line_id BIGINT NOT NULL REFERENCES construction_estimate_line(id) ON DELETE CASCADE,
    successor_line_id   BIGINT NOT NULL REFERENCES construction_estimate_line(id) ON DELETE CASCADE,
    lag_days            INTEGER NOT NULL DEFAULT 0,
    created_by          UUID,
    created_date        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_work_dependency UNIQUE (predecessor_line_id, successor_line_id),
    CONSTRAINT chk_work_dep_not_self CHECK (predecessor_line_id <> successor_line_id)
);

CREATE INDEX IF NOT EXISTS idx_work_dep_project
    ON construction_work_dependencies (tenant_id, project_id);
CREATE INDEX IF NOT EXISTS idx_work_dep_successor
    ON construction_work_dependencies (successor_line_id);

-- RBAC: yangi permission YO'Q — grafik o'qish construction:project:read,
-- yozish construction:estimate:update ostida (rol-grant lockout xavfsiz).
