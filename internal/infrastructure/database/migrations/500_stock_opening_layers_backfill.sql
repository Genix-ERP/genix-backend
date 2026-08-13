-- 500_stock_opening_layers_backfill.sql
--
-- Zaxiralarni baholash rejasi, §6 "Mavjud ma'lumotlar migratsiyasi".
--
-- NIMA UCHUN
-- Qatlamlar modeli (484-migratsiya) jonli bazaga qo'shildi: mavjud har bir
-- ombor qoldig'i undan OLDIN paydo bo'lgan, ya'ni qatlami yo'q. Konveyer endi
-- har bir harakatda ishga tushadi (valuation_hook.go) va FIFO chiqimda
-- iste'mol qiladigan qatlam qidiradi — qatlam bo'lmasa, hisob amalga oshmaydi.
--
-- Shuning uchun har bir mavjud qoldiqqa bitta "start qatlam" beriladi.
-- Qiymat sifatida ombor kartochkasidagi unit_cost olinadi — bu modul
-- allaqachon shu tovarning tannarxi deb hisoblab kelayotgan raqam, ya'ni
-- baholashni yoqish hech kimning zaxira qiymatini jimgina o'zgartirmaydi.
--
-- PROVODKA YO'Q
-- Reja §6 da start qatlamiga Dt 2910 / Kt qoldiq kiritish provodkasi
-- ko'zda tutilgan. Bu yerda u ATAYLAB yozilmaydi: mavjud qoldiqlar
-- buxgalteriyada allaqachon aks etgan, ikkinchi marta yozilsa 2910 ikki
-- barobar bo'lardi. Qatlamlar hozircha kuzatuv qatlami; §1.3 invarianti va
-- "Buxgalteriya bilan solishtirish" hisoboti farqni ko'rsatib turadi.
--
-- IDEMPOTENT: qatlami bor tovar chetlab o'tiladi, shuning uchun qayta
-- yurgizilsa 0 qator qo'shadi.

INSERT INTO stock_valuation_layers (
    tenant_id, organization_id, product_id, layer_date,
    source_type, source_doc_number,
    quantity, unit_cost, value, remaining_qty, remaining_value
)
SELECT
    b.tenant_id,
    b.organization_id,
    b.product_id,
    CURRENT_DATE,
    'opening_balance',
    'MIGRATION-500',
    b.qty,
    b.unit_cost,
    ROUND(b.qty * b.unit_cost, 2),
    b.qty,
    ROUND(b.qty * b.unit_cost, 2)
FROM (
    SELECT
        i.tenant_id,
        -- Qatlam tovar kesimida yuritiladi (§3.2: o'rtacha kompaniya
        -- bo'yicha), shuning uchun omborlar bo'yicha yig'iladi.
        MIN(i.organization_id::text)::uuid                AS organization_id,
        i.product_id,
        SUM(i.quantity_on_hand)                           AS qty,
        -- Miqdorga tortilgan o'rtacha: turli omborlarda narx har xil bo'lishi
        -- mumkin, oddiy AVG esa katta qoldiqni kichigi bilan tenglashtirardi.
        CASE
            WHEN SUM(i.quantity_on_hand) > 0
                THEN SUM(i.quantity_on_hand * i.unit_cost) / SUM(i.quantity_on_hand)
            ELSE 0
        END                                               AS unit_cost
    FROM inventory i
    WHERE i.quantity_on_hand > 0
      AND i.unit_cost > 0
    GROUP BY i.tenant_id, i.product_id
) b
WHERE b.qty > 0
  AND b.unit_cost > 0
  AND ROUND(b.qty * b.unit_cost, 2) > 0
  AND NOT EXISTS (
      SELECT 1 FROM stock_valuation_layers l
      WHERE l.tenant_id = b.tenant_id
        AND l.product_id = b.product_id
        AND NOT l.is_reversed
  );

-- AVCO holati start qatlamga mos bo'lishi kerak, aks holda birinchi o'rtacha
-- chiqim nol qiymatdan hisoblanardi (§3.2: (Q, V) birlamchi).
-- organization_id NOT NULL va PK ning bir qismi (484-migratsiya izohi):
-- NULL ni nol-UUID ga keltiramiz, aks holda qator umuman tushmaydi.
INSERT INTO product_avco_state (tenant_id, organization_id, product_id, quantity, value, last_unit_cost)
SELECT l.tenant_id,
       COALESCE(l.organization_id, '00000000-0000-0000-0000-000000000000'::uuid),
       l.product_id,
       SUM(l.remaining_qty),
       SUM(l.remaining_value),
       CASE WHEN SUM(l.remaining_qty) > 0
            THEN SUM(l.remaining_value) / SUM(l.remaining_qty)
            ELSE 0 END
FROM stock_valuation_layers l
WHERE l.source_type = 'opening_balance'
  AND l.source_doc_number = 'MIGRATION-500'
  AND NOT l.is_reversed
GROUP BY l.tenant_id, COALESCE(l.organization_id, '00000000-0000-0000-0000-000000000000'::uuid), l.product_id
ON CONFLICT DO NOTHING;
