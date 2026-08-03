-- 454_fa_lifecycle_accounts_fix.sql
--
-- Follow-up to 453: align the fixed-asset lifecycle account DEFAULTS with the
-- chart that actually ships (verified against the live BHMS seed):
--   * 0820 does not exist — the capital-investment leaf is 0810
--     ("Tugallanmagan kapital qo'yilmalar"). The old hardcoded 0820 meant v2
--     CreateAsset could NEVER post on a standard chart (the empty-registry bug).
--   * 9210 (disposal transit) is missing from the simplified seed — added here
--     per organization; 4790 ("Boshqa debitorlik qarzlari") is backfilled for
--     orgs older than the Go seed list and becomes the sale-receivable default.
--   * 9430 in this chart is "Ijara xarajatlari" (rent), not "other operating
--     expenses" — the disposal-loss default moves to 9490
--     ("Boshqa xizmatlar uchun xarajatlar"); tenant-editable in Settings.

-- ---------------------------------------------------------------------------
-- 1. Add missing NSBU accounts per organization (pattern of migration 249).
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    org_record  RECORD;
    type_map    jsonb := '{}';
    type_record RECORD;
BEGIN
    FOR type_record IN SELECT id, code FROM account_types LOOP
        type_map := type_map || jsonb_build_object(type_record.code, type_record.id::text);
    END LOOP;

    FOR org_record IN
        SELECT o.id AS org_id, o.tenant_id
        FROM organizations o
        WHERE o.deleted_at IS NULL
    LOOP
        -- 9210 — Asosiy vositalarning chiqib ketishi (disposal transit; nets to 0)
        INSERT INTO accounts (
            id, tenant_id, organization_id, account_type_id, code, name, description,
            is_bank_account, is_control_account, is_reconcilable,
            current_balance, opening_balance, is_active, created_at, updated_at
        )
        SELECT
            uuid_generate_v4(), org_record.tenant_id, org_record.org_id,
            (type_map->>'OTHER_EXP')::uuid,
            '9210', 'Asosiy vositalarning chiqib ketishi',
            'Disposal of fixed assets - transit account, closes to gain/loss',
            false, false, false, 0, 0, true, NOW(), NOW()
        WHERE NOT EXISTS (
            SELECT 1 FROM accounts
            WHERE tenant_id = org_record.tenant_id
              AND organization_id = org_record.org_id
              AND code = '9210' AND deleted_at IS NULL
        );

        -- 4790 — Boshqa debitorlik qarzlari (sale-of-asset receivable) backfill
        INSERT INTO accounts (
            id, tenant_id, organization_id, account_type_id, code, name, description,
            is_bank_account, is_control_account, is_reconcilable,
            current_balance, opening_balance, is_active, created_at, updated_at
        )
        SELECT
            uuid_generate_v4(), org_record.tenant_id, org_record.org_id,
            (type_map->>'AR')::uuid,
            '4790', 'Boshqa debitorlik qarzlari',
            'Other receivables - e.g. proceeds from fixed-asset sales',
            false, false, false, 0, 0, true, NOW(), NOW()
        WHERE NOT EXISTS (
            SELECT 1 FROM accounts
            WHERE tenant_id = org_record.tenant_id
              AND organization_id = org_record.org_id
              AND code = '4790' AND deleted_at IS NULL
        );
    END LOOP;
END $$;

-- ---------------------------------------------------------------------------
-- 2. Fix fa_settings defaults + rows still carrying the unpostable 453 values.
-- ---------------------------------------------------------------------------
ALTER TABLE fa_settings ALTER COLUMN acquisition_account         SET DEFAULT '0810';
ALTER TABLE fa_settings ALTER COLUMN disposal_loss_account       SET DEFAULT '9490';
ALTER TABLE fa_settings ALTER COLUMN disposal_receivable_account SET DEFAULT '4790';

UPDATE fa_settings SET acquisition_account         = '0810' WHERE acquisition_account         = '0820';
UPDATE fa_settings SET disposal_loss_account       = '9490' WHERE disposal_loss_account       = '9430';
UPDATE fa_settings SET disposal_receivable_account = '4790' WHERE disposal_receivable_account = '4890';
