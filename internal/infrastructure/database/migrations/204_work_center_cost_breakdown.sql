-- Work center detailed cost breakdown fields
-- Input fields for calculating hourly cost
ALTER TABLE work_centers ADD COLUMN IF NOT EXISTS asset_value DECIMAL(15,2) DEFAULT 0;
ALTER TABLE work_centers ADD COLUMN IF NOT EXISTS useful_life_years DECIMAL(5,2) DEFAULT 10;
ALTER TABLE work_centers ADD COLUMN IF NOT EXISTS power_kw DECIMAL(10,2) DEFAULT 0;
ALTER TABLE work_centers ADD COLUMN IF NOT EXISTS electricity_rate DECIMAL(10,2) DEFAULT 0;
ALTER TABLE work_centers ADD COLUMN IF NOT EXISTS annual_maintenance DECIMAL(15,2) DEFAULT 0;
ALTER TABLE work_centers ADD COLUMN IF NOT EXISTS operator_monthly_salary DECIMAL(15,2) DEFAULT 0;

-- Calculated per-hour cost components
ALTER TABLE work_centers ADD COLUMN IF NOT EXISTS depreciation_per_hour DECIMAL(15,2) DEFAULT 0;
ALTER TABLE work_centers ADD COLUMN IF NOT EXISTS electricity_per_hour DECIMAL(15,2) DEFAULT 0;
ALTER TABLE work_centers ADD COLUMN IF NOT EXISTS maintenance_per_hour DECIMAL(15,2) DEFAULT 0;
ALTER TABLE work_centers ADD COLUMN IF NOT EXISTS labor_per_hour DECIMAL(15,2) DEFAULT 0;

-- Create account 2590 (Accrued Machine Costs) for all organizations
DO $$
DECLARE
    org_record RECORD;
    liability_type_id UUID;
    v_now TIMESTAMP WITH TIME ZONE := NOW();
BEGIN
    -- Find the short-term liability account type
    SELECT id INTO liability_type_id FROM account_types
    WHERE code = 'ST_LIAB' LIMIT 1;

    IF liability_type_id IS NULL THEN
        SELECT id INTO liability_type_id FROM account_types
        WHERE LOWER(name) LIKE '%short%term%liab%' OR LOWER(name) LIKE '%current%liab%' LIMIT 1;
    END IF;

    IF liability_type_id IS NOT NULL THEN
        FOR org_record IN
            SELECT o.id AS org_id, o.tenant_id
            FROM organizations o WHERE o.deleted_at IS NULL
        LOOP
            INSERT INTO accounts (
                id, tenant_id, organization_id, account_type_id, code, name, name_uz, name_en, description,
                is_bank_account, is_control_account, is_reconcilable,
                current_balance, opening_balance, is_active, created_at, updated_at
            ) VALUES (
                gen_random_uuid(), org_record.tenant_id, org_record.org_id,
                liability_type_id,
                '2590', 'Accrued Machine Costs', 'Hisoblangan stanok xarajatlari', 'Accrued Machine Costs',
                'Accrued liabilities for machine hour costs in production',
                false, false, false, 0, 0, true, v_now, v_now
            ) ON CONFLICT (tenant_id, organization_id, code) DO NOTHING;
        END LOOP;
    END IF;
END $$;
