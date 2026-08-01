-- seed_ombor_demo.sql — Ombor demo ma'lumotlari (docs/ombor-changelog.md #7)
--
-- 2 ombor, 20 ta qurilish mahsuloti, ~3 oylik hujjat tarixi:
-- kirimlar, chiqimlar, bitta ombordan-omborga ko'chirish (juft leg) va
-- kamomadli inventarizatsiya. Barcha harakatlar avval inventory_transactions
-- (ledger) ga yoziladi, so'ng inventory balanslari AYNAN ledger yig'indisidan
-- hisoblanadi — stock_ledger==balance invarianti (test_23) buzilmaydi.
--
-- Idempotent: tenantda reference_type='demo_seed' ledger yozuvi bo'lsa
-- hech narsa qilmaydi. Ishga tushirish:
--   PGPASSWORD=genix_secret psql -h localhost -U genix -d genixerp -f scripts/seed_ombor_demo.sql

BEGIN;

DO $$
DECLARE
  v_tenant uuid;
  v_user   uuid;
  v_org    uuid;
  v_wh1    uuid;
  v_wh2    uuid;
  v_cat    uuid;
  v_count  uuid;
  seeded   int;
BEGIN
  SELECT u.tenant_id, u.id INTO v_tenant, v_user
  FROM users u WHERE u.email = 'admin@genixerp.com' LIMIT 1;
  IF v_tenant IS NULL THEN
    RAISE NOTICE 'ombor demo: admin@genixerp.com tenant topilmadi — skip';
    RETURN;
  END IF;

  SELECT COUNT(*) INTO seeded FROM inventory_transactions
  WHERE tenant_id = v_tenant AND reference_type = 'demo_seed';
  IF seeded > 0 THEN
    RAISE NOTICE 'ombor demo: allaqachon seed qilingan — skip';
    RETURN;
  END IF;

  SELECT id INTO v_org FROM organizations
  WHERE tenant_id = v_tenant AND deleted_at IS NULL
  ORDER BY created_at LIMIT 1;

  -- ── Omborlar ──────────────────────────────────────────────────────
  SELECT id INTO v_wh1 FROM warehouses
  WHERE tenant_id = v_tenant AND code = 'WH-ASOSIY' AND deleted_at IS NULL;
  IF v_wh1 IS NULL THEN
    INSERT INTO warehouses (id, tenant_id, organization_id, code, name, is_default, is_active, created_at, updated_at)
    VALUES (uuid_generate_v4(), v_tenant, v_org, 'WH-ASOSIY', 'Asosiy ombor', true, true, NOW(), NOW())
    RETURNING id INTO v_wh1;
  END IF;

  SELECT id INTO v_wh2 FROM warehouses
  WHERE tenant_id = v_tenant AND code = 'WH-FILIAL' AND deleted_at IS NULL;
  IF v_wh2 IS NULL THEN
    INSERT INTO warehouses (id, tenant_id, organization_id, code, name, is_default, is_active, created_at, updated_at)
    VALUES (uuid_generate_v4(), v_tenant, v_org, 'WH-FILIAL', 'Filial ombori', false, true, NOW(), NOW())
    RETURNING id INTO v_wh2;
  END IF;

  -- ── Kategoriya ────────────────────────────────────────────────────
  SELECT id INTO v_cat FROM product_categories
  WHERE tenant_id = v_tenant AND name = 'Qurilish materiallari' AND deleted_at IS NULL LIMIT 1;
  IF v_cat IS NULL THEN
    INSERT INTO product_categories (id, tenant_id, code, name, is_active, created_at, updated_at)
    VALUES (uuid_generate_v4(), v_tenant, 'QURILISH', 'Qurilish materiallari', true, NOW(), NOW())
    RETURNING id INTO v_cat;
  END IF;

  -- ── 20 ta qurilish mahsuloti ─────────────────────────────────────
  INSERT INTO products (id, tenant_id, category_id, type, code, sku, name,
                        cost_price, list_price, is_stockable, track_inventory,
                        reorder_point, reorder_quantity, is_purchasable, is_sellable,
                        is_active, created_at, updated_at)
  SELECT uuid_generate_v4(), v_tenant, v_cat, 'product', d.code, d.code, d.name,
         d.cost, d.cost * 1.35, true, true,
         d.reorder, d.reorder * 2, true, true, true, NOW(), NOW()
  FROM (VALUES
    ('QM-001', 'Sement M400 (50kg qop)',        45000, 80),
    ('QM-002', 'Sement M500 (50kg qop)',        52000, 60),
    ('QM-003', 'Armatura A500C d12 (metr)',     11000, 200),
    ('QM-004', 'Armatura A500C d16 (metr)',     18500, 150),
    ('QM-005', 'G''isht qizil M100 (dona)',       950, 5000),
    ('QM-006', 'G''isht silikat (dona)',          800, 4000),
    ('QM-007', 'Profil truba 40x20 (metr)',     16000, 100),
    ('QM-008', 'Profil truba 60x40 (metr)',     26000, 80),
    ('QM-009', 'Shifer 8-to''lqinli (varaq)',   38000, 50),
    ('QM-010', 'Gipsokarton 12.5mm (varaq)',    62000, 40),
    ('QM-011', 'Shpaklyovka Vetonit (25kg)',    95000, 30),
    ('QM-012', 'Emulsiya bo''yoq (10L)',       120000, 20),
    ('QM-013', 'Keramik plitka 60x60 (m2)',     85000, 60),
    ('QM-014', 'Laminat 33-klass (m2)',         98000, 50),
    ('QM-015', 'Penoplast 5sm (varaq)',         28000, 100),
    ('QM-016', 'Mineral vata (rulon)',          145000, 25),
    ('QM-017', 'Elektrod MR-3 (kg)',            32000, 40),
    ('QM-018', 'Metall to''r (rulon)',          75000, 15),
    ('QM-019', 'Quruq qorishma (25kg qop)',     35000, 120),
    ('QM-020', 'Gidroizolyatsiya (rulon)',      110000, 20)
  ) AS d(code, name, cost, reorder)
  WHERE NOT EXISTS (
    SELECT 1 FROM products p
    WHERE p.tenant_id = v_tenant AND p.code = d.code AND p.deleted_at IS NULL
  );

  -- ── Balans qatorlari (0 dan boshlab) ─────────────────────────────
  INSERT INTO inventory (id, tenant_id, organization_id, product_id, warehouse_id,
                         quantity_on_hand, quantity_reserved, unit_cost, created_at, updated_at)
  SELECT uuid_generate_v4(), v_tenant, v_org, p.id, w.wh, 0, 0, p.cost_price, NOW(), NOW()
  FROM products p
  CROSS JOIN (VALUES (v_wh1), (v_wh2)) AS w(wh)
  WHERE p.tenant_id = v_tenant AND p.code LIKE 'QM-%' AND p.deleted_at IS NULL
  ON CONFLICT (tenant_id, product_id, warehouse_id) DO NOTHING;

  -- ── Ledger: 3 oylik hujjat tarixi ────────────────────────────────
  -- rn = mahsulot tartib raqami (1..20); miqdorlar deterministik.
  WITH dp AS (
    SELECT p.id, p.code, p.cost_price,
           row_number() OVER (ORDER BY p.code) AS rn
    FROM products p
    WHERE p.tenant_id = v_tenant AND p.code LIKE 'QM-%' AND p.deleted_at IS NULL
  ),
  moves AS (
    -- Kirim #1 (85 kun oldin, arzonroq partiya)
    SELECT id AS product_id, v_wh1 AS wh, 'receipt'::text AS mtype,
           (100 + rn * 5)::numeric AS qty, (cost_price * 0.95)::numeric AS ucost,
           NOW() - INTERVAL '85 days' AS mdate, 'Kirim: boshlang''ich partiya' AS mreason
    FROM dp
    UNION ALL
    -- Kirim #2 (45 kun oldin)
    SELECT id, v_wh1, 'receipt', (60 + rn * 3)::numeric, cost_price,
           NOW() - INTERVAL '45 days', 'Kirim: qayta to''ldirish'
    FROM dp
    UNION ALL
    -- Chiqim #1 (70 kun oldin)
    SELECT id, v_wh1, 'issue', -(30)::numeric, cost_price,
           NOW() - INTERVAL '70 days', 'Chiqim: obyektga material'
    FROM dp
    UNION ALL
    -- Chiqim #2 (30 kun oldin)
    SELECT id, v_wh1, 'issue', -(40 + rn)::numeric, cost_price,
           NOW() - INTERVAL '30 days', 'Chiqim: savdo'
    FROM dp
    UNION ALL
    -- Chiqim #3 (10 kun oldin)
    SELECT id, v_wh1, 'issue', -(10 + (rn % 5))::numeric, cost_price,
           NOW() - INTERVAL '10 days', 'Chiqim: obyektga material'
    FROM dp
    UNION ALL
    -- Sement M400 uchun katta chiqim — kam-qolgan kartani jonlantiradi
    SELECT id, v_wh1, 'issue', -(60)::numeric, cost_price,
           NOW() - INTERVAL '5 days', 'Chiqim: yirik obyekt'
    FROM dp WHERE rn <= 2
    UNION ALL
    -- Ko'chirish (20 kun oldin): dastlabki 3 mahsulot WH1 -> WH2, juft leg
    SELECT id, v_wh1, 'transfer', -(15)::numeric, cost_price,
           NOW() - INTERVAL '20 days', 'Ko''chirish: filialga'
    FROM dp WHERE rn <= 3
    UNION ALL
    SELECT id, v_wh2, 'transfer', (15)::numeric, cost_price,
           NOW() - INTERVAL '20 days', 'Ko''chirish: filialga'
    FROM dp WHERE rn <= 3
    UNION ALL
    -- Inventarizatsiya kamomadi (5 kun oldin): 4- va 5-mahsulotlarda -3
    SELECT id, v_wh1, 'count', -(3)::numeric, cost_price,
           NOW() - INTERVAL '5 days', 'Inventarizatsiya kamomadi'
    FROM dp WHERE rn IN (4, 5)
  )
  INSERT INTO inventory_transactions (
    id, tenant_id, organization_id, inventory_id, product_id, warehouse_id,
    transaction_type, quantity, unit_cost, total_cost,
    from_warehouse_id, to_warehouse_id,
    reference_type, reason, transaction_date, created_by, created_at
  )
  SELECT uuid_generate_v4(), v_tenant, v_org, inv.id, m.product_id, m.wh,
         m.mtype, m.qty, m.ucost, ABS(m.qty) * m.ucost,
         CASE WHEN m.mtype = 'transfer' THEN v_wh1 END,
         CASE WHEN m.mtype = 'transfer' THEN v_wh2 END,
         'demo_seed', m.mreason, m.mdate, v_user, m.mdate
  FROM moves m
  JOIN inventory inv
    ON inv.tenant_id = v_tenant AND inv.product_id = m.product_id AND inv.warehouse_id = m.wh;

  -- ── Balans = ledger yig'indisi (invariant) ───────────────────────
  UPDATE inventory inv
  SET quantity_on_hand = agg.qty,
      unit_cost = COALESCE(agg.avg_cost, inv.unit_cost),
      last_movement_date = agg.last_date,
      updated_at = NOW()
  FROM (
    SELECT t.inventory_id,
           SUM(t.quantity) AS qty,
           SUM(CASE WHEN t.quantity > 0 THEN t.quantity * t.unit_cost END)
             / NULLIF(SUM(CASE WHEN t.quantity > 0 THEN t.quantity END), 0) AS avg_cost,
           MAX(t.transaction_date) AS last_date
    FROM inventory_transactions t
    WHERE t.tenant_id = v_tenant AND t.reference_type = 'demo_seed'
    GROUP BY t.inventory_id
  ) agg
  WHERE inv.id = agg.inventory_id;

  -- ── Inventarizatsiya hujjati (tugallangan, kamomad bilan) ────────
  INSERT INTO stock_counts (id, tenant_id, organization_id, warehouse_id, count_number,
                            count_type, count_date, status, notes,
                            started_at, started_by, completed_at, completed_by,
                            created_by, created_at, updated_at)
  VALUES (uuid_generate_v4(), v_tenant, v_org, v_wh1, 'INV-DEMO-001',
          'partial', (NOW() - INTERVAL '5 days')::date, 'completed',
          'Demo inventarizatsiya — 2 ta mahsulotda kamomad',
          NOW() - INTERVAL '5 days', v_user, NOW() - INTERVAL '5 days', v_user,
          v_user, NOW() - INTERVAL '5 days', NOW() - INTERVAL '5 days')
  RETURNING id INTO v_count;

  INSERT INTO stock_count_lines (id, tenant_id, stock_count_id, product_id,
                                 system_quantity, counted_quantity, unit_cost,
                                 status, created_at, updated_at)
  SELECT uuid_generate_v4(), v_tenant, v_count, dp.id,
         dp2.qty + 3, dp2.qty, dp.cost_price, 'adjusted',
         NOW() - INTERVAL '5 days', NOW() - INTERVAL '5 days'
  FROM (
    SELECT p.id, p.cost_price, row_number() OVER (ORDER BY p.code) AS rn
    FROM products p
    WHERE p.tenant_id = v_tenant AND p.code LIKE 'QM-%' AND p.deleted_at IS NULL
  ) dp
  JOIN LATERAL (
    SELECT inv.quantity_on_hand AS qty FROM inventory inv
    WHERE inv.tenant_id = v_tenant AND inv.product_id = dp.id AND inv.warehouse_id = v_wh1
  ) dp2 ON true
  WHERE dp.rn IN (4, 5);

  RAISE NOTICE 'ombor demo: seed OK (tenant %, 20 mahsulot, 2 ombor)', v_tenant;
END $$;

COMMIT;
