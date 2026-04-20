-- Migration: Enhance Chart of Accounts per BHMS #21 Technical Specification
-- Adds: is_leaf flag, account_nature, analytics/subkonto types, group accounts
-- Reference: TT Buxgalteriya ERP sections 2.1, 4.2, 4.5

-- ============================================
-- STEP 1: Add new columns to accounts table
-- ============================================

-- is_leaf: only leaf accounts can be posted to (TT 4.2)
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS is_leaf BOOLEAN DEFAULT true;

-- account_nature: ACTIVE, PASSIVE, or ACTIVE_PASSIVE per BHMS (TT 2.1)
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS account_nature VARCHAR(20) DEFAULT 'ACTIVE';

-- analytics_types: which subkonto/analytics dimensions are required (TT 4.5)
-- Stored as JSONB array, e.g. ["kontragent", "shartnoma", "ombor"]
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS analytics_types JSONB DEFAULT '[]';

-- mandatory_analytics: if true, posting requires all analytics_types filled (TT 4.5)
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS mandatory_analytics BOOLEAN DEFAULT false;

-- ============================================
-- STEP 2: Create group (parent) accounts for BHMS sections
-- ============================================
-- Group accounts are non-leaf: they cannot be posted to directly
-- They serve as organizational headers in the chart of accounts

DO $$
DECLARE
    org_rec RECORD;
    type_map JSONB := '{}';
    type_record RECORD;
    grp RECORD;
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
        -- Insert group accounts (is_leaf = false)
        FOR grp IN
            SELECT * FROM (VALUES
                -- Section 0: Long-term assets
                ('0000', 'Uzoq muddatli aktivlar', 'Long-term Assets', 'Долгосрочные активы', 'FA', 'ACTIVE'),
                ('0100', 'Asosiy vositalar', 'Fixed Assets', 'Основные средства', 'FA', 'ACTIVE'),
                ('0200', 'Asosiy vositalar eskirishi', 'Depreciation of Fixed Assets', 'Износ основных средств', 'CONTRA_ASSET', 'PASSIVE'),
                ('0400', 'Nomoddiy aktivlar', 'Intangible Assets', 'Нематериальные активы', 'OA', 'ACTIVE'),
                ('0800', 'Kapital qo''yilmalar', 'Capital Investments', 'Капитальные вложения', 'FA', 'ACTIVE'),

                -- Section 1: Inventory
                ('1000', 'Tovar-moddiy zaxiralar', 'Inventories', 'Товарно-материальные запасы', 'INV', 'ACTIVE'),

                -- Section 2: Production costs & finished goods
                ('2000', 'Ishlab chiqarish xarajatlari', 'Production Costs', 'Затраты на производство', 'INV', 'ACTIVE'),
                ('2800', 'Tayyor mahsulot va tovarlar', 'Finished Goods and Merchandise', 'Готовая продукция и товары', 'INV', 'ACTIVE'),
                ('2900', 'Sotib olingan tovarlar', 'Purchased Goods', 'Приобретённые товары', 'INV', 'ACTIVE'),

                -- Section 3: Deferred expenses
                ('3000', 'Kelgusi davr xarajatlari', 'Deferred Expenses', 'Расходы будущих периодов', 'OA', 'ACTIVE'),

                -- Section 4: Receivables
                ('4000', 'Debitorlik qarzdorligi', 'Receivables', 'Дебиторская задолженность', 'AR', 'ACTIVE'),
                ('4200', 'Hisobdor shaxslar bilan hisob-kitob', 'Settlements with Accountable Persons', 'Расчёты с подотчётными лицами', 'OA', 'ACTIVE'),
                ('4400', 'Byudjet bilan hisob-kitob', 'Budget Settlements', 'Расчёты с бюджетом', 'OA', 'ACTIVE_PASSIVE'),
                ('4700', 'Turli debitorlar', 'Various Debtors', 'Разные дебиторы', 'OA', 'ACTIVE'),

                -- Section 5: Cash and cash equivalents
                ('5000', 'Pul mablag''lari', 'Cash and Cash Equivalents', 'Денежные средства', 'CASH', 'ACTIVE'),
                ('5100', 'Bank hisob raqamlari', 'Bank Accounts', 'Банковские счета', 'CASH', 'ACTIVE'),
                ('5200', 'Maxsus bank hisobvaraqlari', 'Special Bank Accounts', 'Специальные банковские счета', 'CASH', 'ACTIVE'),
                ('5500', 'Qisqa muddatli moliyaviy qo''yilmalar', 'Short-term Financial Investments', 'Краткосрочные финансовые вложения', 'OA', 'ACTIVE'),

                -- Section 6: Short-term liabilities
                ('6000', 'Qisqa muddatli majburiyatlar', 'Short-term Liabilities', 'Краткосрочные обязательства', 'AP', 'PASSIVE'),
                ('6100', 'Bo''naklar', 'Advances', 'Авансы полученные', 'ST_LIAB', 'PASSIVE'),
                ('6200', 'Qisqa muddatli kreditlar', 'Short-term Loans', 'Краткосрочные кредиты', 'ST_LIAB', 'PASSIVE'),
                ('6400', 'Byudjetga to''lovlar', 'Tax Payments', 'Расчёты с бюджетом', 'ST_LIAB', 'PASSIVE'),
                ('6500', 'Ijtimoiy sug''urta', 'Social Insurance', 'Социальное страхование', 'ST_LIAB', 'PASSIVE'),
                ('6700', 'Xodimlar bilan hisob-kitob', 'Employee Settlements', 'Расчёты с персоналом', 'ST_LIAB', 'PASSIVE'),
                ('6800', 'Qisqa muddatli kreditlar va qarzlar', 'Short-term Credits', 'Краткосрочные кредиты и займы', 'ST_LIAB', 'PASSIVE'),
                ('6900', 'Boshqa majburiyatlar', 'Other Liabilities', 'Прочие обязательства', 'ST_LIAB', 'PASSIVE'),

                -- Section 7: Long-term liabilities
                ('7000', 'Uzoq muddatli majburiyatlar', 'Long-term Liabilities', 'Долгосрочные обязательства', 'LT_LIAB', 'PASSIVE'),
                ('7300', 'Uzoq muddatli bo''naklar', 'Long-term Advances', 'Долгосрочные авансы', 'LT_LIAB', 'PASSIVE'),
                ('7800', 'Uzoq muddatli kreditlar', 'Long-term Credits', 'Долгосрочные кредиты', 'LT_LIAB', 'PASSIVE'),

                -- Section 8: Equity
                ('8000', 'Kapital', 'Equity', 'Собственный капитал', 'EQUITY', 'PASSIVE'),

                -- Section 9: Revenue and expenses
                ('9000', 'Daromadlar va xarajatlar', 'Revenue and Expenses', 'Доходы и расходы', 'REVENUE', 'ACTIVE_PASSIVE'),
                ('9100', 'Tannarx', 'Cost of Goods Sold', 'Себестоимость', 'COGS', 'ACTIVE'),
                ('9200', 'Boshqa daromadlar', 'Other Income', 'Прочие доходы', 'OTHER_INC', 'PASSIVE'),
                ('9300', 'Boshqa operatsion daromadlar', 'Other Operating Income', 'Прочие операционные доходы', 'OTHER_INC', 'PASSIVE'),
                ('9400', 'Davr xarajatlari', 'Period Expenses', 'Расходы периода', 'OPEX', 'ACTIVE'),
                ('9500', 'Moliyaviy daromadlar', 'Financial Income', 'Финансовые доходы', 'OTHER_INC', 'PASSIVE'),
                ('9600', 'Moliyaviy xarajatlar', 'Financial Expenses', 'Финансовые расходы', 'OTHER_EXP', 'ACTIVE'),
                ('9700', 'Favqulodda foyda va zarar', 'Extraordinary Gains and Losses', 'Чрезвычайные прибыли и убытки', 'OTHER_EXP', 'ACTIVE_PASSIVE'),
                ('9800', 'Favqulodda xarajatlar', 'Extraordinary Expenses', 'Чрезвычайные расходы', 'OTHER_EXP', 'ACTIVE'),
                ('9900', 'Yakuniy moliyaviy natija', 'Final Financial Result', 'Итоговый финансовый результат', 'EQUITY', 'ACTIVE_PASSIVE')
            ) AS t(code, name_uz, name_en, name_ru, type_code, nature)
        LOOP
            -- Idempotent upsert: handles
            --  1. fresh install (INSERT succeeds),
            --  2. re-run / retry (existing active row → DO UPDATE branch runs),
            --  3. soft-deleted row still present with same (tenant, org, code)
            --     → ON CONFLICT fires; WHERE deleted_at IS NULL prevents
            --     touching soft-deleted rows, so it becomes effectively DO NOTHING.
            -- Using ON CONFLICT avoids the previous IF-EXISTS/INSERT race where
            -- the existence check filtered by deleted_at IS NULL but the UNIQUE
            -- constraint does not, causing a duplicate-key failure on install.
            INSERT INTO accounts (
                id, tenant_id, organization_id, account_type_id,
                code, name, name_en, name_ru,
                is_bank_account, is_control_account, is_reconcilable,
                current_balance, opening_balance, is_active, is_leaf, account_nature,
                created_at, updated_at
            ) VALUES (
                uuid_generate_v4(), org_rec.tenant_id, org_rec.org_id,
                (type_map->>grp.type_code)::uuid,
                grp.code, grp.name_uz, grp.name_en, grp.name_ru,
                false, false, false,
                0, 0, true, false, grp.nature,
                v_now, v_now
            )
            ON CONFLICT (tenant_id, organization_id, code) DO UPDATE
                SET is_leaf = false,
                    account_nature = EXCLUDED.account_nature,
                    name_en = COALESCE(NULLIF(accounts.name_en, ''), EXCLUDED.name_en),
                    name_ru = COALESCE(NULLIF(accounts.name_ru, ''), EXCLUDED.name_ru),
                    updated_at = EXCLUDED.updated_at
                WHERE accounts.deleted_at IS NULL;
        END LOOP;

        -- ============================================
        -- STEP 3: Set parent_id for existing leaf accounts to their group
        -- ============================================
        -- Link leaf accounts to their group parent based on code prefix
        -- e.g. 0110 → parent is 0100, 5010 → parent is 5000

        -- Link 0xxx sub-accounts to their group
        UPDATE accounts a SET parent_id = g.id
        FROM accounts g
        WHERE a.organization_id = org_rec.org_id
          AND g.organization_id = org_rec.org_id
          AND a.deleted_at IS NULL AND g.deleted_at IS NULL
          AND a.parent_id IS NULL
          AND a.code ~ '^0[1-9][0-9]{1,2}$'
          AND g.code = LEFT(a.code, 2) || '00'
          AND a.id != g.id;

        -- Link section groups (0100, 0200, etc.) to section header (0000)
        UPDATE accounts a SET parent_id = g.id
        FROM accounts g
        WHERE a.organization_id = org_rec.org_id
          AND g.organization_id = org_rec.org_id
          AND a.deleted_at IS NULL AND g.deleted_at IS NULL
          AND a.parent_id IS NULL
          AND a.code ~ '^0[0-9]00$'
          AND g.code = '0000'
          AND a.id != g.id
          AND a.code != '0000';

        -- Same for sections 1xxx through 9xxx
        UPDATE accounts a SET parent_id = g.id
        FROM accounts g
        WHERE a.organization_id = org_rec.org_id
          AND g.organization_id = org_rec.org_id
          AND a.deleted_at IS NULL AND g.deleted_at IS NULL
          AND a.parent_id IS NULL
          AND a.code ~ '^[1-9][0-9]{2,3}$'
          AND LENGTH(a.code) = 4
          AND g.code = LEFT(a.code, 2) || '00'
          AND a.id != g.id
          AND EXISTS (SELECT 1 FROM accounts WHERE code = LEFT(a.code, 2) || '00' AND organization_id = org_rec.org_id AND deleted_at IS NULL);

        -- Link section sub-groups to their section header
        UPDATE accounts a SET parent_id = g.id
        FROM accounts g
        WHERE a.organization_id = org_rec.org_id
          AND g.organization_id = org_rec.org_id
          AND a.deleted_at IS NULL AND g.deleted_at IS NULL
          AND a.parent_id IS NULL
          AND a.code ~ '^[1-9][0-9]00$'
          AND g.code = LEFT(a.code, 1) || '000'
          AND a.id != g.id
          AND a.code != LEFT(a.code, 1) || '000'
          AND EXISTS (SELECT 1 FROM accounts WHERE code = LEFT(a.code, 1) || '000' AND organization_id = org_rec.org_id AND deleted_at IS NULL);

    END LOOP;
END $$;

-- ============================================
-- STEP 4: Set account_nature for existing leaf accounts
-- ============================================
UPDATE accounts SET account_nature = CASE
    -- Active accounts (debit normal balance)
    WHEN code ~ '^0[1]' THEN 'ACTIVE'          -- Fixed assets
    WHEN code ~ '^0[4]' THEN 'ACTIVE'          -- Intangible assets
    WHEN code ~ '^08' THEN 'ACTIVE'            -- Capital investments
    WHEN code ~ '^1[0-9]' THEN 'ACTIVE'        -- Inventory
    WHEN code ~ '^2[0-8]' THEN 'ACTIVE'        -- Production & finished goods
    WHEN code ~ '^29' THEN 'ACTIVE'            -- Purchased goods
    WHEN code ~ '^3[0-1]' THEN 'ACTIVE'        -- Deferred expenses
    WHEN code ~ '^4[0-9]' THEN 'ACTIVE'        -- Receivables (mostly active)
    WHEN code ~ '^5[0-5]' THEN 'ACTIVE'        -- Cash
    WHEN code ~ '^91' THEN 'ACTIVE'            -- Cost of goods sold
    WHEN code ~ '^94' THEN 'ACTIVE'            -- Period expenses
    WHEN code ~ '^96' THEN 'ACTIVE'            -- Financial expenses
    WHEN code ~ '^97' THEN 'ACTIVE'            -- Extraordinary losses
    WHEN code ~ '^98' THEN 'ACTIVE'            -- Extraordinary expenses

    -- Passive accounts (credit normal balance)
    WHEN code ~ '^02' THEN 'PASSIVE'           -- Depreciation
    WHEN code ~ '^04[89]' THEN 'PASSIVE'       -- Intangible depreciation
    WHEN code ~ '^6[0-9]' THEN 'PASSIVE'       -- Short-term liabilities
    WHEN code ~ '^7[0-9]' THEN 'PASSIVE'       -- Long-term liabilities
    WHEN code ~ '^8[0-9]' THEN 'PASSIVE'       -- Equity
    WHEN code ~ '^90' THEN 'PASSIVE'           -- Revenue
    WHEN code ~ '^92' THEN 'PASSIVE'           -- Other income
    WHEN code ~ '^93' THEN 'PASSIVE'           -- Other operating income
    WHEN code ~ '^95' THEN 'PASSIVE'           -- Financial income

    -- Active-passive
    WHEN code ~ '^44' THEN 'ACTIVE_PASSIVE'    -- Budget settlements (can be both)
    WHEN code ~ '^99' THEN 'ACTIVE_PASSIVE'    -- Financial result
    ELSE 'ACTIVE'
END
WHERE deleted_at IS NULL AND account_nature = 'ACTIVE';

-- ============================================
-- STEP 5: Set analytics_types for accounts requiring mandatory analytics (TT 4.5)
-- ============================================
-- 4010 (Xaridorlar) → kontragent, shartnoma
UPDATE accounts SET analytics_types = '["kontragent", "shartnoma"]'::jsonb, mandatory_analytics = true
WHERE code = '4010' AND deleted_at IS NULL;

-- 6010 (Ta'minotchilar) → kontragent, shartnoma
UPDATE accounts SET analytics_types = '["kontragent", "shartnoma"]'::jsonb, mandatory_analytics = true
WHERE code = '6010' AND deleted_at IS NULL;

-- 2910 (Tovarlar) → ombor
UPDATE accounts SET analytics_types = '["ombor"]'::jsonb, mandatory_analytics = true
WHERE code IN ('2910', '2810') AND deleted_at IS NULL;

-- 4710 (Hisobdor shaxslar) → xodim
UPDATE accounts SET analytics_types = '["xodim"]'::jsonb, mandatory_analytics = true
WHERE code = '4710' AND deleted_at IS NULL;

-- 4210 (Hisobdor shaxs bo'naklari) → xodim
UPDATE accounts SET analytics_types = '["xodim"]'::jsonb, mandatory_analytics = true
WHERE code = '4210' AND deleted_at IS NULL;

-- 6710 (Ish haqi) → xodim
UPDATE accounts SET analytics_types = '["xodim"]'::jsonb, mandatory_analytics = true
WHERE code = '6710' AND deleted_at IS NULL;

-- 1010 (Xom ashyo) → ombor
UPDATE accounts SET analytics_types = '["ombor"]'::jsonb, mandatory_analytics = true
WHERE code IN ('1010', '1020', '1030', '1040', '1050', '1060', '1080', '1090') AND deleted_at IS NULL;

-- ============================================
-- STEP 6: Ensure group accounts are marked as non-leaf
-- ============================================
-- Any account that has children should be non-leaf
UPDATE accounts a SET is_leaf = false
WHERE EXISTS (
    SELECT 1 FROM accounts c
    WHERE c.parent_id = a.id AND c.deleted_at IS NULL
)
AND a.deleted_at IS NULL;
