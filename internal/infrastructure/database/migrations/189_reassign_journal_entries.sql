-- Reassign existing journal entries from MISC/GENERAL to their correct journals
-- based on entry_number prefix / reference patterns.
-- This runs AFTER migration 188 which creates STOCK, ASSET, CONST, PURCH, PAYROLL journals.

DO $$
DECLARE
    tnt RECORD;
    v_stock_id UUID;
    v_asset_id UUID;
    v_const_id UUID;
    v_purch_id UUID;
    v_payroll_id UUID;
BEGIN
    FOR tnt IN SELECT id FROM tenants LOOP
        -- Look up target journals for this tenant
        SELECT id INTO v_stock_id   FROM journals WHERE tenant_id = tnt.id AND code = 'STOCK'   AND deleted_at IS NULL LIMIT 1;
        SELECT id INTO v_asset_id   FROM journals WHERE tenant_id = tnt.id AND code = 'ASSET'   AND deleted_at IS NULL LIMIT 1;
        SELECT id INTO v_const_id   FROM journals WHERE tenant_id = tnt.id AND code = 'CONST'   AND deleted_at IS NULL LIMIT 1;
        SELECT id INTO v_purch_id   FROM journals WHERE tenant_id = tnt.id AND code = 'PURCH'   AND deleted_at IS NULL LIMIT 1;
        SELECT id INTO v_payroll_id FROM journals WHERE tenant_id = tnt.id AND code = 'PAYROLL' AND deleted_at IS NULL LIMIT 1;

        -- Stock operations: REC*, DEL*, ADJ*, MFG*, INV* prefixes → STOCK journal
        IF v_stock_id IS NOT NULL THEN
            UPDATE journal_entries
            SET journal_id = v_stock_id
            WHERE tenant_id = tnt.id
              AND journal_id IN (SELECT id FROM journals WHERE tenant_id = tnt.id AND code IN ('MISC','GEN','GENERAL') AND deleted_at IS NULL)
              AND (entry_number LIKE 'REC%' OR entry_number LIKE 'DEL%' OR entry_number LIKE 'ADJ%'
                   OR entry_number LIKE 'MFG%' OR entry_number LIKE 'INV%'
                   OR reference LIKE 'Stock Operation%' OR reference LIKE 'Inventory Adjustment%'
                   OR reference LIKE 'Goods Delivery%' OR reference LIKE 'Production Order%');
        END IF;

        -- Fixed asset operations: FA* prefix → ASSET journal
        IF v_asset_id IS NOT NULL THEN
            UPDATE journal_entries
            SET journal_id = v_asset_id
            WHERE tenant_id = tnt.id
              AND journal_id IN (SELECT id FROM journals WHERE tenant_id = tnt.id AND code IN ('MISC','GEN','GENERAL') AND deleted_at IS NULL)
              AND (entry_number LIKE 'FA%' OR reference LIKE 'Fixed Asset%' OR reference LIKE 'Asset %');
        END IF;

        -- Construction operations: MR* prefix → CONST journal
        IF v_const_id IS NOT NULL THEN
            UPDATE journal_entries
            SET journal_id = v_const_id
            WHERE tenant_id = tnt.id
              AND journal_id IN (SELECT id FROM journals WHERE tenant_id = tnt.id AND code IN ('MISC','GEN','GENERAL') AND deleted_at IS NULL)
              AND (entry_number LIKE 'MR%' OR reference LIKE 'Construction Material%' OR reference LIKE 'Material Request%');
        END IF;

        -- Purchase operations: PUR*, PI* prefix → PURCH journal
        IF v_purch_id IS NOT NULL THEN
            UPDATE journal_entries
            SET journal_id = v_purch_id
            WHERE tenant_id = tnt.id
              AND journal_id IN (SELECT id FROM journals WHERE tenant_id = tnt.id AND code IN ('MISC','GEN','GENERAL') AND deleted_at IS NULL)
              AND (entry_number LIKE 'PUR%' OR entry_number LIKE 'PI%' OR reference LIKE 'Purchase%');
        END IF;

        -- Payroll operations: PAY* prefix → PAYROLL journal
        IF v_payroll_id IS NOT NULL THEN
            UPDATE journal_entries
            SET journal_id = v_payroll_id
            WHERE tenant_id = tnt.id
              AND journal_id IN (SELECT id FROM journals WHERE tenant_id = tnt.id AND code IN ('MISC','GEN','GENERAL') AND deleted_at IS NULL)
              AND (entry_number LIKE 'PAY%' OR reference LIKE 'Payroll%' OR reference LIKE 'Salary%');
        END IF;
    END LOOP;
END $$;
