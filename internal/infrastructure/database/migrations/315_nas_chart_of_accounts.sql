-- Migration: Align chart of accounts with Uzbekistan NAS (National Accounting Standard)
-- Reference: https://lex.uz/acts/1357627 (Buxgalteriya hisobi schyotlar rejasi)
-- This migration:
--   1. Renames existing account codes to match NAS standard
--   2. Updates account names to Uzbek NAS names
--   3. Adds essential NAS accounts that were missing

-- ============================================
-- STEP 1: Remap existing account codes to NAS
-- ============================================
-- We must update codes carefully to avoid unique constraint violations.
-- Strategy: first rename to temp codes, then to final NAS codes.

DO $$
DECLARE
    org_rec RECORD;
    mapping RECORD;
BEGIN
    -- Process each organization separately
    FOR org_rec IN
        SELECT DISTINCT organization_id FROM accounts WHERE deleted_at IS NULL AND organization_id IS NOT NULL
    LOOP
        -- Phase 1: Rename to temporary codes (prefix with 'T') to avoid conflicts
        -- Only rename if the old_code exists AND the temp_code doesn't already exist
        FOR mapping IN
            SELECT * FROM (VALUES
                ('1000', 'T5010'),
                ('1010', 'T5020'),
                ('1020', 'T5030'),
                ('1100', 'T5110'),
                ('1200', 'T4010'),
                ('1210', 'T4910'),
                ('1300', 'T1010'),
                ('1310', 'T1030'),
                ('1320', 'T2010'),
                ('1330', 'T2810'),
                ('1340', 'T2910'),
                ('1400', 'T3100'),
                ('1410', 'T4410'),
                ('1500', 'T0100'),
                ('1510', 'T0200'),
                ('1600', 'T0400'),
                ('1700', 'T0810'),
                ('1730', 'T4210'),
                ('2000', 'T6010'),
                ('2100', 'T6990'),
                ('2110', 'T6710'),
                ('2120', 'T6920'),
                ('2200', 'T6410'),
                ('2210', 'T6420'),
                ('2220', 'T6430'),
                ('2230', 'T6010a'),
                ('2231', 'T6010b'),
                ('2300', 'T6310'),
                ('2400', 'T6810'),
                ('2500', 'T7810'),
                ('2590', 'T2590t'),
                ('2930', 'T3110'),
                ('3000', 'T8300'),
                ('3100', 'T8310'),
                ('3200', 'T8700'),
                ('3300', 'T9910'),
                ('3400', 'T8720'),
                ('3500', 'T8710'),
                ('3600', 'T9920'),
                ('4000', 'T9010'),
                ('4100', 'T9030'),
                ('4300', 'T9020'),
                ('4900', 'T9310'),
                ('4910', 'T9510'),
                ('4920', 'T9540'),
                ('5000', 'T9110'),
                ('5100', 'T9120'),
                ('5200', 'T9130'),
                ('5300', 'T9140'),
                ('6000', 'T9420'),
                ('6100', 'T9430'),
                ('6200', 'T9440'),
                ('6300', 'T9450'),
                ('6400', 'T9460'),
                ('6500', 'T9470'),
                ('6600', 'T9480'),
                ('6700', 'T9490'),
                ('6800', 'T9495'),
                ('6900', 'T9410'),
                ('7000', 'T9610'),
                ('7100', 'T9620'),
                ('7200', 'T9630'),
                ('7900', 'T9690')
            ) AS t(old_code, temp_code)
        LOOP
            -- Only rename if old_code exists and temp_code does not already exist for this org
            IF EXISTS (
                SELECT 1 FROM accounts WHERE code = mapping.old_code AND organization_id = org_rec.organization_id AND deleted_at IS NULL
            ) AND NOT EXISTS (
                SELECT 1 FROM accounts WHERE code = mapping.temp_code AND organization_id = org_rec.organization_id AND deleted_at IS NULL
            ) THEN
                UPDATE accounts
                SET code = mapping.temp_code, updated_at = NOW()
                WHERE code = mapping.old_code
                  AND organization_id = org_rec.organization_id
                  AND deleted_at IS NULL;
            END IF;
        END LOOP;

        -- Phase 2: Rename temp codes to final NAS codes and update names
        FOR mapping IN
            SELECT * FROM (VALUES
                ('T5010', '5010', 'Kassa', 'Naqd pul kassada'),
                ('T5020', '5020', 'Valyuta kassasi', 'Chet el valyutasidagi naqd pullar'),
                ('T5030', '5030', 'Yo''ldagi pul o''tkazmalari', 'Yo''ldagi pul o''tkazmalari'),
                ('T5110', '5110', 'Hisob-kitob schyoti', 'Asosiy bank hisob raqami'),
                ('T4010', '4010', 'Xaridor va buyurtmachilardan olinadigan schyotlar', 'Savdo debitorlik qarzlari'),
                ('T4910', '4910', 'Shubhali qarzlar bo''yicha zaxira', 'Shubhali qarzlar uchun zaxira'),
                ('T1010', '1010', 'Xom ashyo va materiallar', 'Tovar-moddiy zaxiralar'),
                ('T1030', '1030', 'Yoqilg''i', 'Xom ashyo materiallari'),
                ('T2010', '2010', 'Asosiy ishlab chiqarish', 'Tugallanmagan ishlab chiqarish'),
                ('T2810', '2810', 'Tayyor mahsulot', 'Tayyor mahsulot omborda'),
                ('T2910', '2910', 'Sotib olingan tovarlar', 'Qayta sotish uchun tovarlar'),
                ('T3100', '3100', 'Kelgusi davr xarajatlari', 'Oldindan to''langan xarajatlar'),
                ('T4410', '4410', 'Byudjetga soliqlar bo''yicha bo''nak to''lovlari', 'QQS kirim (olinadigan)'),
                ('T0100', '0100', 'Asosiy vositalar', 'Asosiy vositalar (mol-mulk, zavod va jihozlar)'),
                ('T0200', '0200', 'Asosiy vositalar eskirishi', 'Yig''ilgan eskirish'),
                ('T0400', '0400', 'Nomoddiy aktivlar', 'Nomoddiy aktivlar'),
                ('T0810', '0810', 'Tugallanmagan kapital qo''yilmalar', 'Qurilish ishlari xarajatlari'),
                ('T4210', '4210', 'Mehnat haqi bo''yicha berilgan bo''naklar', 'Xodimlarga berilgan bo''naklar'),
                ('T6010', '6010', 'Mol yetkazib beruvchilar va pudratchilar', 'Mol yetkazib beruvchilarga savdo kreditorlik qarzlari'),
                ('T6990', '6990', 'Boshqa kreditorlik qarzlari', 'Hisoblangan majburiyatlar'),
                ('T6710', '6710', 'Mehnat haqi bo''yicha xodimlarga bo''lgan qarz', 'Ish haqi va maoshlar to''lanishi kerak'),
                ('T6920', '6920', 'Foizlar bo''yicha hisoblashlar', 'To''lanishi kerak bo''lgan foizlar'),
                ('T6410', '6410', 'Byudjetga to''lovlar bo''yicha qarz (turlar bo''yicha)', 'Soliq majburiyatlari'),
                ('T6420', '6420', 'QQS bo''yicha qarz', 'QQS/Savdo solig''i to''lanishi kerak'),
                ('T6430', '6430', 'Foyda solig''i bo''yicha qarz', 'Daromad solig''i to''lanishi kerak'),
                ('T6010a', '6015', 'Olingan, lekin hisob-faktura qilinmagan tovarlar', 'Olingan, lekin hali hisob-faktura ko''rsatilmagan tovarlar uchun oraliq hisob'),
                ('T6010b', '6016', 'Yetkazilgan, lekin hisob-faktura qilinmagan tovarlar', 'Yetkazilgan, lekin hali hisob-faktura ko''rsatilmagan tovarlar uchun oraliq hisob'),
                ('T6310', '6310', 'Kelgusi davr daromadlari', 'Kechiktirilgan daromad'),
                ('T6810', '6810', 'Qisqa muddatli bank kreditlari', 'Qisqa muddatli qarzlar'),
                ('T7810', '7810', 'Uzoq muddatli bank kreditlari', 'Uzoq muddatli qarzlar'),
                ('T2590t', '2590', 'Mashina xarajatlari', 'Ishlab chiqarishda mashina soat xarajatlari uchun hisoblangan majburiyatlar'),
                ('T3110', '3110', 'Kelgusi davr xarajatlari (uzoq muddatli)', 'Kelgusi davr xarajatlari'),
                ('T8300', '8300', 'Ustav kapitali', 'Egasining kapitali'),
                ('T8310', '8310', 'Oddiy aksiyalar', 'Chiqarilgan aksiyadorlik kapitali'),
                ('T8700', '8700', 'Taqsimlanmagan foyda (qoplanmagan zarar)', 'Yig''ilgan foyda'),
                ('T9910', '9910', 'Yakuniy moliyaviy natija', 'Joriy davr foydasi/zarari'),
                ('T8720', '8720', 'E''lon qilingan dividendlar', 'E''lon qilingan dividendlar'),
                ('T8710', '8710', 'Taqsimlanmagan foyda (oldingi yillar)', 'Taqsimlanmagan foyda'),
                ('T9920', '9920', 'Joriy yil foydasi', 'Joriy yil foydasi'),
                ('T9010', '9010', 'Tayyor mahsulot sotishdan daromadlar', 'Sotishdan tushum'),
                ('T9030', '9030', 'Xizmatlar ko''rsatishdan daromadlar', 'Xizmatlardan tushum'),
                ('T9020', '9020', 'Tovarlar sotishdan daromadlar', 'Qurilish loyihalaridan tushum'),
                ('T9310', '9310', 'Boshqa operatsion daromadlar', 'Turli xil daromadlar'),
                ('T9510', '9510', 'Foiz shaklida daromadlar', 'Olingan foizlar'),
                ('T9540', '9540', 'Valyuta kursi farqlaridan daromadlar', 'Valyuta ayirboshlash bo''yicha foyda'),
                ('T9110', '9110', 'Sotilgan tayyor mahsulot tannarxi', 'Sotilgan tovarlarning to''g''ridan-to''g''ri tannarxi'),
                ('T9120', '9120', 'Sotilgan tovarlar tannarxi', 'Ishlatilgan xom ashyo tannarxi'),
                ('T9130', '9130', 'Ishlab chiqarish xarajatlari', 'Bevosita mehnat xarajatlari'),
                ('T9140', '9140', 'Umumishlab chiqarish xarajatlari', 'Ishlab chiqarish ustama xarajatlari'),
                ('T9420', '9420', 'Mehnat haqi xarajatlari', 'Xodimlar ish haqi va maoshlari'),
                ('T9430', '9430', 'Ijara xarajatlari', 'Ijara va lizing to''lovlari'),
                ('T9440', '9440', 'Kommunal xarajatlar', 'Elektr, suv, gaz'),
                ('T9450', '9450', 'Ofis xarajatlari', 'Ofis jihozlari xarajati'),
                ('T9460', '9460', 'Sug''urta xarajatlari', 'Sug''urta mukofotlari'),
                ('T9470', '9470', 'Eskirish xarajatlari', 'Aktivlar eskirishi'),
                ('T9480', '9480', 'Reklama va marketing xarajatlari', 'Marketing xarajatlari'),
                ('T9490', '9490', 'Boshqa xizmatlar uchun xarajatlar', 'Yuridik, buxgalteriya to''lovlari'),
                ('T9495', '9495', 'Xizmat safari va vakillik xarajatlari', 'Biznes safari xarajatlari'),
                ('T9410', '9410', 'Davr xarajatlari', 'Boshqa operatsion xarajatlar'),
                ('T9610', '9610', 'Foiz shaklida xarajatlar', 'Qarzlar bo''yicha foizlar'),
                ('T9620', '9620', 'Bank xizmatlari uchun xarajatlar', 'Bank to''lovlari va yig''imlar'),
                ('T9630', '9630', 'Valyuta kursi farqlaridan zararlar', 'Valyuta ayirboshlashda zarar'),
                ('T9690', '9690', 'Boshqa moliyaviy xarajatlar', 'Turli xil xarajatlar')
            ) AS t(temp_code, new_code, new_name, new_desc)
        LOOP
            -- Only rename if temp_code exists and new_code does not already exist for this org
            IF EXISTS (
                SELECT 1 FROM accounts WHERE code = mapping.temp_code AND organization_id = org_rec.organization_id AND deleted_at IS NULL
            ) AND NOT EXISTS (
                SELECT 1 FROM accounts WHERE code = mapping.new_code AND organization_id = org_rec.organization_id AND deleted_at IS NULL
            ) THEN
                UPDATE accounts
                SET code = mapping.new_code,
                    name = mapping.new_name,
                    description = mapping.new_desc,
                    updated_at = NOW()
                WHERE code = mapping.temp_code
                  AND organization_id = org_rec.organization_id
                  AND deleted_at IS NULL;
            ELSIF EXISTS (
                SELECT 1 FROM accounts WHERE code = mapping.temp_code AND organization_id = org_rec.organization_id AND deleted_at IS NULL
            ) THEN
                -- Target code already exists: update the existing account's name/desc and soft-delete the temp one
                UPDATE accounts
                SET name = mapping.new_name,
                    description = mapping.new_desc,
                    updated_at = NOW()
                WHERE code = mapping.new_code
                  AND organization_id = org_rec.organization_id
                  AND deleted_at IS NULL;
                -- Soft-delete the orphaned temp account
                UPDATE accounts
                SET deleted_at = NOW(), updated_at = NOW()
                WHERE code = mapping.temp_code
                  AND organization_id = org_rec.organization_id
                  AND deleted_at IS NULL;
            END IF;
        END LOOP;
    END LOOP;
END $$;

-- ============================================
-- STEP 2: Add missing essential NAS accounts
-- ============================================
-- These are commonly used NAS accounts that GenixERP didn't have

DO $$
DECLARE
    org_rec RECORD;
    type_map JSONB := '{}';
    type_record RECORD;
    acc RECORD;
    v_now TIMESTAMP WITH TIME ZONE := NOW();
BEGIN
    -- Build account type map
    FOR type_record IN SELECT id, code FROM account_types LOOP
        type_map := type_map || jsonb_build_object(type_record.code, type_record.id::text);
    END LOOP;

    FOR org_rec IN
        SELECT DISTINCT o.id AS org_id, o.tenant_id
        FROM organizations o
        WHERE o.deleted_at IS NULL
    LOOP
        FOR acc IN
            SELECT * FROM (VALUES
                -- Fixed assets sub-accounts
                ('0110', 'Yer', 'FA', 'Yer uchastkasi'),
                ('0120', 'Binolar, inshootlar va uzatuvchi moslamalar', 'FA', 'Binolar va inshootlar'),
                ('0130', 'Mashina va asbob-uskunalar', 'FA', 'Mashina va uskunalar'),
                ('0140', 'Mebel va ofis jihozlari', 'FA', 'Mebel va ofis jihozlari'),
                ('0150', 'Kompyuter jihozlari va hisoblash texnikasi', 'FA', 'Kompyuter jihozlari'),
                ('0160', 'Transport vositalari', 'FA', 'Transport vositalari'),
                -- Depreciation sub-accounts
                ('0220', 'Bino va inshootlarning eskirishi', 'CONTRA_ASSET', 'Bino va inshootlar eskirishi'),
                ('0230', 'Mashina va asbob-uskunalarning eskirishi', 'CONTRA_ASSET', 'Mashina uskunalar eskirishi'),
                ('0260', 'Transport vositalarining eskirishi', 'CONTRA_ASSET', 'Transport eskirishi'),
                -- Intangible assets sub-accounts
                ('0410', 'Patentlar, litsenziyalar, nou-xau', 'OA', 'Patentlar va litsenziyalar'),
                ('0420', 'Savdo belgisi', 'OA', 'Savdo belgisi va brend'),
                ('0430', 'Tashkiliy xarajatlar', 'OA', 'Tashkiliy xarajatlar'),
                ('0490', 'Nomoddiy aktivlar eskirishi', 'CONTRA_ASSET', 'Nomoddiy aktivlar amortizatsiyasi'),
                -- Inventory sub-accounts
                ('1020', 'Sotib olingan yarim tayyor mahsulotlar', 'INV', 'Sotib olingan yarim tayyor mahsulotlar'),
                ('1040', 'Idish va idish materiallari', 'INV', 'Idish va qadoqlash materiallari'),
                ('1050', 'Ehtiyot qismlar', 'INV', 'Ehtiyot qismlar'),
                ('1060', 'Qurilish materiallari', 'INV', 'Qurilish materiallari'),
                ('1080', 'Qishloq xo''jaligi mahsulotlari', 'INV', 'Qishloq xo''jaligi materiallari'),
                ('1090', 'Boshqa materiallar', 'INV', 'Boshqa materiallar'),
                -- Production
                ('2310', 'Yordamchi ishlab chiqarish', 'INV', 'Yordamchi ishlab chiqarish xarajatlari'),
                ('2510', 'Umumishlab chiqarish xarajatlari', 'OPEX', 'Ishlab chiqarish ustama xarajatlari'),
                ('2610', 'Nosoz mahsulot uchun yo''qotishlar', 'OPEX', 'Brak va nosozlik xarajatlari'),
                -- Cash
                ('5110', 'Hisob-kitob schyoti', 'CASH', 'Asosiy bank hisob raqami'),
                ('5210', 'Maxsus bank hisobvaraqlari', 'CASH', 'Maxsus bank hisobvaraqlari'),
                ('5520', 'Qisqa muddatli obligatsiyalar', 'OA', 'Qisqa muddatli obligatsiyalar'),
                ('5510', 'Depozit sertifikatlari', 'OA', 'Depozit sertifikatlari'),
                -- Receivables
                ('4020', 'Ajratilgan bo''linmalardan olinadigan schyotlar', 'AR', 'Ichki bo''linmalardan olinadigan schyotlar'),
                ('4310', 'TMQ uchun berilgan bo''naklar', 'OA', 'Mol yetkazib beruvchilarga bo''naklar'),
                ('4320', 'Uzoq muddatli aktivlar uchun berilgan bo''naklar', 'OA', 'Uzoq muddatli aktivlar uchun bo''naklar'),
                ('4710', 'Hisobdor shaxslar', 'OA', 'Hisobdor shaxslarga berilgan summa'),
                ('4790', 'Boshqa debitorlik qarzlari', 'AR', 'Boshqa debitorlik qarzlari'),
                ('4800', 'Uzoq muddatli debitorlik qarzlari', 'AR', 'Uzoq muddatli debitorlik qarzlari'),
                -- Payables
                ('6020', 'Ajratilgan bo''linmalarga bo''lgan qarz', 'AP', 'Ichki bo''linmalarga qarz'),
                ('6110', 'Olingan bo''naklar', 'ST_LIAB', 'Oldindan olingan to''lovlar'),
                ('6210', 'Qisqa muddatli bank kreditlari', 'ST_LIAB', 'Qisqa muddatli kreditlar'),
                ('6220', 'Qisqa muddatli qarzlar', 'ST_LIAB', 'Qisqa muddatli qarzlar'),
                ('6510', 'Maqsadli davlat jamg''armalariga to''lovlar', 'ST_LIAB', 'Ijtimoiy sug''urta to''lovlari'),
                ('6520', 'Sug''urta bo''yicha to''lovlar', 'ST_LIAB', 'Sug''urta bo''yicha to''lovlar'),
                -- Long-term
                ('7310', 'Xaridorlardan olingan bo''naklar', 'LT_LIAB', 'Uzoq muddatli oldindan olingan to''lovlar'),
                ('7820', 'Uzoq muddatli qarzlar', 'LT_LIAB', 'Uzoq muddatli qarzlar'),
                -- Equity
                ('8400', 'Zaxira kapitali', 'EQUITY', 'Zaxira kapitali'),
                ('8500', 'Qo''shimcha kapital', 'EQUITY', 'Qo''shimcha kapital'),
                ('8600', 'Maqsadli tushumlar', 'EQUITY', 'Maqsadli tushumlar'),
                -- Revenue/Expense additional
                ('9040', 'Qurilish shartnomasi bo''yicha daromadlar', 'REVENUE', 'Qurilish loyihalaridan tushum'),
                ('9210', 'Asosiy vositalarni sotishdan foyda', 'OTHER_INC', 'Asosiy vositalar sotishdan foyda'),
                ('9220', 'Boshqa aktivlarni sotishdan foyda', 'OTHER_INC', 'Boshqa aktivlarni sotish foydasi'),
                ('9320', 'Bepul olingan mol-mulk', 'OTHER_INC', 'Tekin olingan aktivlar'),
                ('9340', 'O''tgan yillar operatsiyalari bo''yicha foyda', 'OTHER_INC', 'O''tgan yillar bo''yicha foyda'),
                ('9350', 'Kreditorlik qarzlarini hisobdan chiqarish', 'OTHER_INC', 'Eskirgan kreditorlik qarzlari'),
                ('9150', 'Xizmatlar tannarxi', 'COGS', 'Ko''rsatilgan xizmatlar tannarxi'),
                ('9160', 'Qurilish ishlari tannarxi', 'COGS', 'Qurilish ishlari tannarxi'),
                ('9710', 'Asosiy vositalarni sotishdan zarar', 'OTHER_EXP', 'Asosiy vositalar sotish zarari'),
                ('9720', 'Boshqa aktivlarni sotishdan zarar', 'OTHER_EXP', 'Boshqa aktivlar sotish zarari'),
                ('9810', 'Favqulodda zarar', 'OTHER_EXP', 'Favqulodda xarajatlar'),
                ('9820', 'Foyda solig''i bo''yicha xarajatlar', 'OTHER_EXP', 'Foyda solig''i xarajati')
            ) AS t(code, name, type_code, description)
        LOOP
            INSERT INTO accounts (
                id, tenant_id, organization_id, account_type_id, code, name, description,
                is_bank_account, is_control_account, is_reconcilable,
                current_balance, opening_balance, is_active, created_at, updated_at
            ) VALUES (
                uuid_generate_v4(), org_rec.tenant_id, org_rec.org_id,
                (type_map->>acc.type_code)::uuid,
                acc.code, acc.name, acc.description,
                CASE WHEN acc.type_code = 'CASH' THEN true ELSE false END,
                CASE WHEN acc.type_code IN ('AR', 'AP') THEN true ELSE false END,
                CASE WHEN acc.type_code IN ('CASH', 'AR', 'AP') THEN true ELSE false END,
                0, 0, true, v_now, v_now
            )
            ON CONFLICT (tenant_id, organization_id, code) DO NOTHING;
        END LOOP;
    END LOOP;
END $$;

-- ============================================
-- STEP 3: Update journal_entry_lines account references
-- ============================================
-- Journal entry lines reference accounts by account_id (UUID), not by code,
-- so they automatically follow the code rename. No action needed.

-- ============================================
-- STEP 4: Update any account references in journals table
-- ============================================
-- Journals reference accounts by account_id (UUID), so they also follow automatically.
