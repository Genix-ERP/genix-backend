-- 495_ish_grafigi_avtoreja.sql — Ish grafigini avtomatik rejalashtirish (v1).
--
-- Muammo: grafik smetadan quriladi, lekin sanalar qo'lda qo'yiladi — real
-- loyihada 2904 ishdan 2900 tasi "Rejalashtirilmagan". Bu migratsiya avtomatik
-- rejalashtirish uchun kerakli maydonlarni qo'shadi: davomiylik (norma bo'yicha
-- hisoblangan), bog'liqlik manbasi, kalendar, loyiha parametrlari va
-- yurgizishlar jurnali (orqaga qaytarish uchun diff bilan).
--
-- MODEL QARORI (475 bilan bir xil): alohida "schedule" jadvali YO'Q — sanalar
-- ish qatorining o'zida (construction_estimate_line, ish =
-- resource_type='' AND parent_line_id=0). Divergensiya bo'lmaydi.
--
-- FAKT/STATUS: yangi status maydonlari YARATILMAYDI. 353-migratsiyadagi
-- approval_status (pending → in_progress → submitted → confirmed_*) TZ §5.1
-- hayot sikliga aynan mos keladi, done_quantity esa fakt hajmi. Rejalashtiruvchi
-- shulardan foydalanadi:
--   boshlangan (siljitilmaydi) := approval_status <> 'pending' OR done_quantity > 0
--   tugagan   (daxlsiz)        := approval_status IN ('submitted',
--                                  'confirmed_supervisor','confirmed_engineer')
--   muddati o'tgan             := sched_end < server_today
--                                  AND approval_status IN ('pending','in_progress')

-- ---------------------------------------------------------------------------
-- 1. Ish qatori: davomiylik va sana manbasi (TZ §1.1)
-- ---------------------------------------------------------------------------
ALTER TABLE construction_estimate_line
    -- Ish kunlaridagi davomiylik. Sanalarni generatsiya qilish uchun KIRISH
    -- ma'lumoti (475 da davomiylik end-start dan hisoblanardi; avtoreja uchun
    -- teskarisi kerak: davomiylikdan sanalar).
    ADD COLUMN IF NOT EXISTS duration_days   INTEGER,
    -- norm | productivity | default | manual  (TZ §2 kaskadi)
    ADD COLUMN IF NOT EXISTS duration_source VARCHAR(20),
    -- Hisob paytidagi norma snapshot'i: spravochnik keyin o'zgarsa ham
    -- hisoblangan ishlar qayta yozilmaydi (TZ §2 oxirgi qoida).
    ADD COLUMN IF NOT EXISTS norm_snapshot   JSONB,
    -- none | auto | manual — qo'lda qo'yilgan sana daxlsiz (TZ §0.3)
    ADD COLUMN IF NOT EXISTS schedule_source VARCHAR(10) NOT NULL DEFAULT 'none',
    -- "Sanani qotirish": avtomat hech qachon siljitmaydi
    ADD COLUMN IF NOT EXISTS is_fixed        BOOLEAN NOT NULL DEFAULT false;

-- Mavjud sanali qatorlar manual hisoblanadi — birinchi yurgizish ularni
-- siljitib yubormasligi uchun (aks holda qo'lda kiritilgan 4 ta sana yo'qoladi).
UPDATE construction_estimate_line
SET schedule_source = 'manual'
WHERE schedule_source = 'none' AND (sched_start IS NOT NULL OR sched_end IS NOT NULL);

CREATE INDEX IF NOT EXISTS idx_estimate_line_sched_source
    ON construction_estimate_line (estimate_id, schedule_source);

-- ---------------------------------------------------------------------------
-- 2. Bog'liqliklar: tur va manba (TZ §1.2)
-- ---------------------------------------------------------------------------
ALTER TABLE construction_work_dependencies
    -- v1: faqat FS. SS/FF — v2.
    ADD COLUMN IF NOT EXISTS dep_type   VARCHAR(4)  NOT NULL DEFAULT 'FS',
    -- auto: har yurgizishda qayta quriladi; manual: foydalanuvchi yaratgan,
    -- yurgizish tegmaydi lekin hisobda hurmat qiladi.
    ADD COLUMN IF NOT EXISTS dep_source VARCHAR(10) NOT NULL DEFAULT 'manual';

CREATE INDEX IF NOT EXISTS idx_work_dep_source
    ON construction_work_dependencies (project_id, dep_source);

-- ---------------------------------------------------------------------------
-- 3. Loyiha rejalashtirish parametrlari (TZ §1.3)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS construction_schedule_params (
    project_id      BIGINT PRIMARY KEY REFERENCES construction_projects(id) ON DELETE CASCADE,
    tenant_id       UUID   NOT NULL REFERENCES tenants(id),
    start_date      DATE,
    -- Bo'limda parallel yuradigan ishlar soni ≈ brigadalar soni.
    parallel_limit  INTEGER NOT NULL DEFAULT 2  CHECK (parallel_limit BETWEEN 1 AND 50),
    crew_size       INTEGER NOT NULL DEFAULT 4  CHECK (crew_size > 0),
    hours_per_shift NUMERIC(5,2) NOT NULL DEFAULT 8 CHECK (hours_per_shift > 0),
    shifts          INTEGER NOT NULL DEFAULT 1  CHECK (shifts > 0),
    -- Hafta ish kunlari bitmask: bit0=Dushanba … bit6=Yakshanba.
    -- Default 63 = Du–Sh (qurilishda odatiy), Yakshanba dam.
    workdays_mask   INTEGER NOT NULL DEFAULT 63 CHECK (workdays_mask BETWEEN 1 AND 127),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- 4. Bayramlar (kompaniya spravochnigi) — kalendar uchun
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS construction_calendar_holidays (
    id           BIGSERIAL PRIMARY KEY,
    tenant_id    UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    holiday_date DATE NOT NULL,
    name         VARCHAR(200) NOT NULL DEFAULT '',
    UNIQUE (tenant_id, holiday_date)
);

-- ---------------------------------------------------------------------------
-- 5. Unumdorlik normalari spravochnigi (kaskadning 2-bosqichi, TZ §2)
--    Rastsenkasiz pozitsiyalar uchun: ish turi/kod → birlikka kishi-soat.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS construction_productivity_norms (
    id                 BIGSERIAL PRIMARY KEY,
    tenant_id          UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    -- Rastsenka kodi yoki ish nomidagi kalit so'z (ILIKE bo'yicha moslash).
    match_code         VARCHAR(100) NOT NULL DEFAULT '',
    match_name         VARCHAR(300) NOT NULL DEFAULT '',
    uom                VARCHAR(50)  NOT NULL DEFAULT '',
    man_hours_per_unit NUMERIC(18,6) NOT NULL CHECK (man_hours_per_unit > 0),
    is_active          BOOLEAN NOT NULL DEFAULT true,
    created_date       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_productivity_norms_tenant
    ON construction_productivity_norms (tenant_id, is_active);

-- ---------------------------------------------------------------------------
-- 6. Yurgizishlar jurnali (TZ §1.4, §6.5) — orqaga qaytarish uchun diff bilan
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS construction_schedule_run (
    id                 BIGSERIAL PRIMARY KEY,
    tenant_id          UUID   NOT NULL REFERENCES tenants(id),
    project_id         BIGINT NOT NULL REFERENCES construction_projects(id) ON DELETE CASCADE,
    run_kind           VARCHAR(20) NOT NULL DEFAULT 'auto',  -- auto | recalc | undo
    params             JSONB  NOT NULL DEFAULT '{}',
    scope              VARCHAR(30) NOT NULL DEFAULT 'unplanned',
    affected_count     INTEGER NOT NULL DEFAULT 0,
    project_end_before DATE,
    project_end_after  DATE,
    -- [{line_id, start_before, end_before, start_after, end_after,
    --   source_before, source_after, duration_before, duration_after}]
    diff_snapshot      JSONB  NOT NULL DEFAULT '[]',
    undone_at          TIMESTAMPTZ,
    created_by         UUID,
    created_date       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_schedule_run_project
    ON construction_schedule_run (tenant_id, project_id, created_date DESC);

-- RBAC: yangi permission YO'Q — 475 bilan bir xil, o'qish construction:project:read,
-- yozish construction:estimate:update ostida.
