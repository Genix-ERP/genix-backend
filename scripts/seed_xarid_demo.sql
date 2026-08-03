-- seed_xarid_demo.sql — Xarid demo ma'lumotlari (docs/xarid-changelog.md "Qolgan" §8).
--
-- Dev-DB uchun: birinchi tenant'ning mavjud vendor/product'laridan foydalanib
-- 6 ta PO (draft ×1, approved ×1, approved-overdue ×1, partial ×1,
-- received ×1, cancelled ×1) + vendor_prices va supplier_price_history
-- yozuvlarini yaratadi — dashboard, chartlar va Narxlar solishtiruvi jonli
-- ko'rinishi uchun. Idempotent emas — qayta yuritilsa yangi PO'lar qo'shiladi.
-- Ishga tushirish: psql "$DB_DSN" -f scripts/seed_xarid_demo.sql

DO $$
DECLARE
    t UUID;
    org UUID;
    vendors UUID[];
    products UUID[];
    v UUID;
    p UUID;
    po_id UUID;
    i INT;
    statuses TEXT[] := ARRAY['draft', 'approved', 'approved', 'partial', 'received', 'cancelled'];
    st TEXT;
    odate DATE;
    edate DATE;
    qty NUMERIC;
    price NUMERIC;
    recv NUMERIC;
BEGIN
    SELECT id INTO t FROM tenants WHERE deleted_at IS NULL ORDER BY created_at LIMIT 1;
    IF t IS NULL THEN RAISE NOTICE 'seed_xarid_demo: tenant topilmadi'; RETURN; END IF;

    SELECT organization_id INTO org FROM purchase_orders
    WHERE tenant_id = t AND organization_id IS NOT NULL LIMIT 1;

    SELECT ARRAY(SELECT id FROM contacts
        WHERE tenant_id = t AND type IN ('vendor', 'both') AND deleted_at IS NULL
        ORDER BY created_at LIMIT 8) INTO vendors;
    SELECT ARRAY(SELECT id FROM products
        WHERE tenant_id = t AND deleted_at IS NULL AND is_active = true
        ORDER BY created_at LIMIT 8) INTO products;

    IF COALESCE(array_length(vendors, 1), 0) < 2 OR COALESCE(array_length(products, 1), 0) < 2 THEN
        RAISE NOTICE 'seed_xarid_demo: vendor/product yetarli emas (vendor %, product %)',
            COALESCE(array_length(vendors, 1), 0), COALESCE(array_length(products, 1), 0);
        RETURN;
    END IF;

    -- Narx ro'yxatlari + tarix: har product uchun 2 vendor, ozgina farqli narxlar
    FOR i IN 1..LEAST(array_length(products, 1), 6) LOOP
        p := products[i];
        FOR v IN SELECT unnest(vendors[1:2]) LOOP
            price := 50000 + i * 12000 + (('x' || substr(md5(v::text || p::text), 1, 4))::bit(16)::int % 9000);
            INSERT INTO vendor_prices (id, tenant_id, organization_id, vendor_id, product_id,
                price, currency, min_quantity, lead_time_days, is_active, valid_from, created_at, updated_at)
            SELECT uuid_generate_v4(), t, org, v, p, price, 'UZS', 1, 3 + (i % 5), true, CURRENT_DATE - 60, NOW(), NOW()
            WHERE NOT EXISTS (
                SELECT 1 FROM vendor_prices vp WHERE vp.tenant_id = t AND vp.vendor_id = v
                  AND vp.product_id = p AND vp.deleted_at IS NULL AND vp.is_active = true
            );
            INSERT INTO supplier_price_history (id, tenant_id, organization_id, product_id, vendor_id,
                unit_price, effective_date, source, created_at)
            VALUES
                (uuid_generate_v4(), t, org, p, v, price * 0.95, CURRENT_DATE - 90, 'seed_demo', NOW()),
                (uuid_generate_v4(), t, org, p, v, price, CURRENT_DATE - 20, 'seed_demo', NOW());
        END LOOP;
    END LOOP;

    -- 6 ta PO turli statuslarda, order_date oxirgi 5 oyga taqsimlangan
    FOR i IN 1..6 LOOP
        st := statuses[i];
        v := vendors[1 + (i % array_length(vendors, 1))];
        p := products[1 + (i % array_length(products, 1))];
        odate := CURRENT_DATE - (i - 1) * 28;
        edate := CASE
            WHEN i = 3 THEN CURRENT_DATE - 7   -- approved + o'tgan sana = kechikkan yetkazma
            ELSE odate + 10
        END;
        qty := 10 * i;
        price := 60000 + i * 15000;
        recv := CASE st WHEN 'received' THEN qty WHEN 'partial' THEN ROUND(qty / 2.0) ELSE 0 END;

        po_id := uuid_generate_v4();
        INSERT INTO purchase_orders (id, tenant_id, organization_id, order_number, vendor_id,
            order_date, expected_date, subtotal, total_amount, status, payment_status,
            notes, created_at, updated_at)
        VALUES (po_id, t, org, 'PO-DEMO-' || to_char(NOW(), 'MMDD') || '-' || i || '-' || substr(md5(random()::text), 1, 4),
            v, odate, edate, qty * price, qty * price, st, 'unpaid',
            'Demo seed (seed_xarid_demo.sql)', odate::timestamp, NOW());

        INSERT INTO purchase_order_lines (id, purchase_order_id, line_number, product_id,
            description, quantity, unit_price, line_total, quantity_received, created_at, updated_at)
        VALUES (uuid_generate_v4(), po_id, 1, p, 'Demo qator', qty, price, qty * price, recv, NOW(), NOW());
    END LOOP;

    RAISE NOTICE 'seed_xarid_demo: 6 PO + narx yozuvlari yaratildi (tenant %)', t;
END $$;
