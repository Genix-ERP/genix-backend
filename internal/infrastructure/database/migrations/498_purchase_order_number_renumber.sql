-- 498_purchase_order_number_renumber.sql
--
-- Xarid buyurtmalari raqami PO-1771931368445 ko'rinishida chiqib qolgan.
-- Sabab: raqam generatori MAX(raqam)+1 bilan ishlaydi, va bir marta epoch-ms
-- (Date.now()) dan kelib chiqqan 13 xonali raqam bazaga tushgach, MAX abadiy
-- o'sha ulkan sondan boshlaydigan bo'lgan — keyingi har bir buyurtma
-- ...440, ...441, ...442 bo'lib ketgan. `PO-%05d` formati bu yerda hech narsa
-- qilmaydi, chunki son allaqachon 5 xonadan uzun.
--
-- Bu migratsiya faqat ANIQ buzilgan qatorlarni tozalaydi:
--   • order_number `PO-` + 7 va undan ortiq raqam, VA
--   • sonning qiymati 1 000 000 dan katta
-- Ya'ni qo'lda qo'yilgan normal raqamlar (PO-001, PO-1234, hatto PO-0000001)
-- tegilmaydi — hech bir tenant o'z raqamlashini yo'qotmaydi.
--
-- Renumbering (tenant, organization) kesimida, created_at tartibida boradi va
-- o'sha kesimdagi eng katta "sog'lom" raqamdan keyin davom etadi, shuning
-- uchun purchase_orders_tenant_org_order_number_key bilan to'qnashmaydi.
-- Yangi format: PO-001, PO-002 … PO-999, keyin PO-1000 — uzunlik o'zi o'sadi.

WITH sane AS (
    SELECT tenant_id,
           organization_id,
           MAX((regexp_replace(order_number, '[^0-9]', '', 'g'))::bigint) AS max_num
    FROM purchase_orders
    WHERE order_number ~ '^PO-[0-9]{1,6}$'
    GROUP BY tenant_id, organization_id
),
poisoned AS (
    SELECT id,
           tenant_id,
           organization_id,
           ROW_NUMBER() OVER (
               PARTITION BY tenant_id, organization_id
               ORDER BY created_at, id
           ) AS rn
    FROM purchase_orders
    WHERE order_number ~ '^PO-[0-9]{7,}$'
      AND (regexp_replace(order_number, '[^0-9]', '', 'g'))::bigint > 1000000
),
renum AS (
    -- DIQQAT: LPAD berilgan uzunlikdan uzun matnni QIRQIB tashlaydi
    -- (LPAD('1235', 3, '0') → '123'), shuning uchun 999 dan katta raqamlarda
    -- LPAD ishlatib bo'lmaydi. Go tarafdagi %03d bunday qilmaydi — u faqat
    -- minimal kenglikni ta'minlaydi; bu yerda ham xuddi shu xulq kerak.
    SELECT p.id,
           'PO-' || CASE
               WHEN COALESCE(s.max_num, 0) + p.rn < 1000
                   THEN LPAD((COALESCE(s.max_num, 0) + p.rn)::text, 3, '0')
               ELSE (COALESCE(s.max_num, 0) + p.rn)::text
           END AS new_number
    FROM poisoned p
    LEFT JOIN sane s
           ON s.tenant_id = p.tenant_id
          AND s.organization_id IS NOT DISTINCT FROM p.organization_id
)
UPDATE purchase_orders po
SET order_number = r.new_number
FROM renum r
WHERE po.id = r.id;

-- Denormalizatsiya qilingan nusxalarni ham yangilaymiz. Faqat buzilgan
-- raqamni saqlab turgan qatorlar tegiladi, shuning uchun qayta ishga
-- tushirilsa ham zarari yo'q (idempotent).
--
-- DIQQAT: sales_orders.po_number / sales_invoices.po_number bu yerga
-- kirmaydi — ular MIJOZNING o'z buyurtma raqami, bizniki emas.

UPDATE goods_receipts gr
SET po_number = po.order_number
FROM purchase_orders po
WHERE po.id = gr.purchase_order_id
  AND gr.po_number ~ '^PO-[0-9]{7,}$';

UPDATE purchase_returns pr
SET po_number = po.order_number
FROM purchase_orders po
WHERE po.id = pr.purchase_order_id
  AND pr.po_number ~ '^PO-[0-9]{7,}$';

UPDATE landed_costs lc
SET po_number = po.order_number
FROM purchase_orders po
WHERE po.id = lc.purchase_order_id
  AND lc.po_number ~ '^PO-[0-9]{7,}$';
