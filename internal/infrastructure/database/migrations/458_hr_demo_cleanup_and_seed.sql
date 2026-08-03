-- 458: HR demo tozalash + realistik seed
-- 1) Integratsion testlar qoldirgan xodimlarni soft-delete qilish.
--    test_21/test_22 xodimlari QAT'IY 'EMP-T21-'/'EMP-T22-' prefiksi bilan
--    yaratiladi (tests/test_21_ish_haqi.py, test_22_ish_haqi_privacy.py) —
--    boshqa hech narsa bu prefikslarni ishlatmaydi.
-- 2) Demo tenant uchun bo'limlar, lavozimlar va realistik xodimlar to'plami
--    (bo'sh departments/job_positions jadvallari "other" badge sizig'ining
--    ildiz sababi edi). Barcha bloklar idempotent.

UPDATE employees
SET deleted_at = NOW(), updated_at = NOW()
WHERE deleted_at IS NULL
  AND (employee_number LIKE 'EMP-T21-%' OR employee_number LIKE 'EMP-T22-%');

DO $$
DECLARE
    v_tid UUID := 'df372dc3-0b77-4ec6-aef3-c1145ecbeaac';
    v_org UUID;
    d_mgmt UUID; d_acc UUID; d_sales UUID; d_const UUID; d_prod UUID;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM tenants WHERE id = v_tid) THEN
        SELECT id INTO v_tid FROM tenants WHERE name = 'Demo Company' ORDER BY created_at LIMIT 1;
    END IF;
    IF v_tid IS NULL THEN
        RAISE NOTICE '458: demo tenant topilmadi — HR seed o''tkazib yuborildi';
        RETURN;
    END IF;

    SELECT id INTO v_org FROM organizations WHERE tenant_id = v_tid AND deleted_at IS NULL ORDER BY created_at LIMIT 1;
    IF v_org IS NULL THEN
        RAISE NOTICE '458: demo organization topilmadi — HR seed o''tkazib yuborildi';
        RETURN;
    END IF;

    -- ── Bo'limlar ──
    INSERT INTO departments (id, tenant_id, organization_id, code, name, is_active, created_at, updated_at)
    VALUES
        (uuid_generate_v4(), v_tid, v_org, 'MGMT',  'Boshqaruv',        true, NOW(), NOW()),
        (uuid_generate_v4(), v_tid, v_org, 'ACC',   'Buxgalteriya',     true, NOW(), NOW()),
        (uuid_generate_v4(), v_tid, v_org, 'SALES', 'Savdo bo''limi',   true, NOW(), NOW()),
        (uuid_generate_v4(), v_tid, v_org, 'CONST', 'Qurilish bo''limi',true, NOW(), NOW()),
        (uuid_generate_v4(), v_tid, v_org, 'PROD',  'Ishlab chiqarish', true, NOW(), NOW())
    ON CONFLICT (tenant_id, organization_id, code) DO NOTHING;

    SELECT id INTO d_mgmt  FROM departments WHERE tenant_id = v_tid AND organization_id = v_org AND code = 'MGMT'  AND deleted_at IS NULL;
    SELECT id INTO d_acc   FROM departments WHERE tenant_id = v_tid AND organization_id = v_org AND code = 'ACC'   AND deleted_at IS NULL;
    SELECT id INTO d_sales FROM departments WHERE tenant_id = v_tid AND organization_id = v_org AND code = 'SALES' AND deleted_at IS NULL;
    SELECT id INTO d_const FROM departments WHERE tenant_id = v_tid AND organization_id = v_org AND code = 'CONST' AND deleted_at IS NULL;
    SELECT id INTO d_prod  FROM departments WHERE tenant_id = v_tid AND organization_id = v_org AND code = 'PROD'  AND deleted_at IS NULL;

    -- ── Lavozimlar ──
    INSERT INTO job_positions (id, tenant_id, organization_id, code, name, is_active, created_at, updated_at)
    VALUES
        (uuid_generate_v4(), v_tid, v_org, 'CHENG',  'Bosh muhandis', true, NOW(), NOW()),
        (uuid_generate_v4(), v_tid, v_org, 'BUX',    'Buxgalter',     true, NOW(), NOW()),
        (uuid_generate_v4(), v_tid, v_org, 'PRORAB', 'Prorab',        true, NOW(), NOW()),
        (uuid_generate_v4(), v_tid, v_org, 'MEN',    'Menejer',       true, NOW(), NOW()),
        (uuid_generate_v4(), v_tid, v_org, 'USTA',   'Usta',          true, NOW(), NOW()),
        (uuid_generate_v4(), v_tid, v_org, 'ISH',    'Ishchi',        true, NOW(), NOW()),
        (uuid_generate_v4(), v_tid, v_org, 'HRM',    'HR menejeri',   true, NOW(), NOW())
    ON CONFLICT (tenant_id, code) DO NOTHING;

    -- ── Realistik xodimlar (12 ta; sanalar CURRENT_DATE'ga nisbatan —
    --     Tahlillar grafigi doim "tirik" ko'rinadi) ──
    INSERT INTO employees (id, tenant_id, organization_id, department_id, job_position_id,
                           employee_number, first_name, last_name, job_title, hire_date,
                           base_salary, status, date_of_birth, probation_end_date,
                           termination_date, termination_reason, created_at, updated_at)
    SELECT uuid_generate_v4(), v_tid, v_org,
           CASE d.dept WHEN 'MGMT' THEN d_mgmt WHEN 'ACC' THEN d_acc WHEN 'SALES' THEN d_sales
                       WHEN 'CONST' THEN d_const ELSE d_prod END,
           (SELECT id FROM job_positions jp WHERE jp.tenant_id = v_tid AND jp.code = d.jp AND jp.deleted_at IS NULL LIMIT 1),
           d.emp_no, d.fn, d.ln, d.title,
           (CURRENT_DATE - (d.hired_mo || ' months')::interval)::date,
           d.salary, d.st,
           d.dob,
           CASE WHEN d.prob_days IS NOT NULL THEN CURRENT_DATE + d.prob_days ELSE NULL END,
           CASE WHEN d.term_mo IS NOT NULL THEN (CURRENT_DATE - (d.term_mo || ' months')::interval)::date ELSE NULL END,
           d.term_reason,
           NOW(), NOW()
    FROM (VALUES
        ('DEMO-EMP-001', 'Dilshod',  'Rahimov',   'Bosh muhandis', 'CHENG',  'CONST', 23, 8000000::numeric, 'active',     DATE '1988-05-14', NULL::int, NULL::int, NULL),
        ('DEMO-EMP-002', 'Aziza',    'Karimova',  'Buxgalter',     'BUX',    'ACC',   21, 6500000, 'active',     DATE '1992-11-02', NULL, NULL, NULL),
        ('DEMO-EMP-003', 'Jasur',    'Toshmatov', 'Prorab',        'PRORAB', 'CONST', 18, 5500000, 'active',     DATE '1990-03-21', NULL, NULL, NULL),
        ('DEMO-EMP-004', 'Malika',   'Yusupova',  'Menejer',       'MEN',    'SALES', 15, 4500000, 'active',     DATE '1995-07-09', NULL, NULL, NULL),
        ('DEMO-EMP-005', 'Sardor',   'Aliyev',    'Usta',          'USTA',   'PROD',  12, 3500000, 'active',     DATE '1993-01-28', NULL, NULL, NULL),
        ('DEMO-EMP-006', 'Nodira',   'Islomova',  'HR menejeri',   'HRM',    'MGMT',  10, 5000000, 'active',     DATE '1991-09-17', NULL, NULL, NULL),
        ('DEMO-EMP-007', 'Bekzod',   'Qodirov',   'Ishchi',        'ISH',    'PROD',   8, 3200000, 'active',     DATE '1997-12-05', NULL, NULL, NULL),
        ('DEMO-EMP-008', 'Gulnora',  'Sattorova', 'Menejer',       'MEN',    'SALES',  6, 4200000, 'on_leave',   DATE '1994-04-11', NULL, NULL, NULL),
        ('DEMO-EMP-009', 'Otabek',   'Ergashev',  'Ishchi',        'ISH',    'CONST',  3, 3200000, 'active',     DATE '1998-08-23', 10,   NULL, NULL),
        ('DEMO-EMP-010', 'Kamola',   'Berdiyeva', 'Buxgalter',     'BUX',    'ACC',    1, 4000000, 'active',     DATE '1996-06-30', 20,   NULL, NULL),
        ('DEMO-EMP-011', 'Rustam',   'Nazarov',   'Ishchi',        'ISH',    'PROD',  20, 3000000, 'terminated', DATE '1989-10-19', NULL, 2,   'Shtat qisqarishi'),
        ('DEMO-EMP-012', 'Shaxnoza', 'Umarova',   'Menejer',       'MEN',    'SALES', 14, 4000000, 'terminated', DATE '1996-02-14', NULL, 1,   'O''z xohishiga ko''ra')
    ) AS d(emp_no, fn, ln, title, jp, dept, hired_mo, salary, st, dob, prob_days, term_mo, term_reason)
    WHERE NOT EXISTS (SELECT 1 FROM employees ex
                      WHERE ex.tenant_id = v_tid AND ex.employee_number = d.emp_no);

    -- Yaqin 30 kun ichida tug'ilgan kun ko'rinishi uchun ikkitasini dinamik qilamiz
    UPDATE employees SET date_of_birth = make_date(1992,
        EXTRACT(MONTH FROM CURRENT_DATE + 12)::int, EXTRACT(DAY FROM CURRENT_DATE + 12)::int)
    WHERE tenant_id = v_tid AND employee_number = 'DEMO-EMP-004';
    UPDATE employees SET date_of_birth = make_date(1992,
        EXTRACT(MONTH FROM CURRENT_DATE + 25)::int, EXTRACT(DAY FROM CURRENT_DATE + 25)::int)
    WHERE tenant_id = v_tid AND employee_number = 'DEMO-EMP-007';

    -- Ilgari seed qilingan bo'limsiz demo xodimlarga bo'lim biriktirish
    UPDATE employees SET department_id = d_prod, updated_at = NOW()
    WHERE tenant_id = v_tid AND deleted_at IS NULL AND department_id IS NULL
      AND employee_number LIKE 'DEMO-EMP-%';
END $$;
