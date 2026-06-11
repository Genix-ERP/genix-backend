-- Migration: Add foreign-currency bank accounts (5210, 5220) per UzNAS chart
-- The 5200 section group exists, but no postable (leaf) foreign-currency bank
-- account was ever seeded: migration 315 only added 5210 for orgs that existed
-- at that time, and the Go-side new-org seeding skipped 52xx leaves entirely.
-- UzNAS (lex.uz/acts/1357627) defines:
--   5210 - Mamlakat ichidagi valyuta schyotlari
--   5220 - Chet eldagi valyuta schyotlari

DO $$
DECLARE
    org_rec RECORD;
    cash_type_id UUID;
    acc RECORD;
    v_now TIMESTAMP WITH TIME ZONE := NOW();
BEGIN
    SELECT id INTO cash_type_id FROM account_types WHERE code = 'CASH' LIMIT 1;
    IF cash_type_id IS NULL THEN
        RAISE NOTICE 'CASH account type not found, skipping forex account seed';
        RETURN;
    END IF;

    FOR org_rec IN
        SELECT o.id AS org_id, o.tenant_id
        FROM organizations o
        WHERE o.deleted_at IS NULL
    LOOP
        FOR acc IN
            SELECT * FROM (VALUES
                ('5210', 'Mamlakat ichidagi valyuta schyotlari', 'Foreign Currency Accounts (Domestic)', 'Валютные счета внутри страны', 'Mamlakat ichidagi banklardagi chet el valyutasi schyotlari'),
                ('5220', 'Chet eldagi valyuta schyotlari', 'Foreign Currency Accounts (Abroad)', 'Валютные счета за рубежом', 'Chet eldagi banklardagi chet el valyutasi schyotlari')
            ) AS t(code, name_uz, name_en, name_ru, description)
        LOOP
            INSERT INTO accounts (
                id, tenant_id, organization_id, account_type_id,
                code, name, name_en, name_ru, description,
                is_bank_account, is_control_account, is_reconcilable,
                current_balance, opening_balance, is_active, is_leaf, account_nature,
                created_at, updated_at
            ) VALUES (
                uuid_generate_v4(), org_rec.tenant_id, org_rec.org_id, cash_type_id,
                acc.code, acc.name_uz, acc.name_en, acc.name_ru, acc.description,
                true, false, true,
                0, 0, true, true, 'ACTIVE',
                v_now, v_now
            )
            ON CONFLICT (tenant_id, organization_id, code) DO NOTHING;
        END LOOP;

        -- Link the new leaves to the 5200 section group where it exists
        UPDATE accounts a SET parent_id = g.id
        FROM accounts g
        WHERE a.tenant_id = org_rec.tenant_id AND a.organization_id = org_rec.org_id
          AND g.tenant_id = org_rec.tenant_id AND g.organization_id = org_rec.org_id
          AND a.deleted_at IS NULL AND g.deleted_at IS NULL
          AND a.parent_id IS NULL
          AND a.code IN ('5210', '5220')
          AND g.code = '5200'
          AND a.id != g.id;
    END LOOP;
END $$;
