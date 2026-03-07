-- Restructure account codes to match O'zbekiston NSBU standards (per TZ HisobRejasi)
-- 1010: "Petty Cash" → "Bank Account" (is_bank_account=true)
-- 1100: "Bank Account" → "Accounts Receivable" (type CASH→AR, is_bank_account=false)
-- 1200: Soft-delete after transferring journal entries to 1100

DO $$
DECLARE
    org_rec RECORD;
    acct_1010_id UUID;
    acct_1100_id UUID;
    acct_1200_id UUID;
    ar_type_id UUID;
    v_now TIMESTAMP WITH TIME ZONE := NOW();
BEGIN
    -- Get the AR account type ID
    SELECT id INTO ar_type_id FROM account_types WHERE code = 'AR';
    IF ar_type_id IS NULL THEN
        RAISE EXCEPTION 'AR account type not found';
    END IF;

    FOR org_rec IN
        SELECT DISTINCT tenant_id, organization_id
        FROM accounts
        WHERE code IN ('1010', '1100', '1200') AND deleted_at IS NULL
        GROUP BY tenant_id, organization_id
    LOOP
        -- Get account IDs for this org
        SELECT id INTO acct_1010_id FROM accounts
        WHERE tenant_id = org_rec.tenant_id AND organization_id = org_rec.organization_id
          AND code = '1010' AND deleted_at IS NULL;

        SELECT id INTO acct_1100_id FROM accounts
        WHERE tenant_id = org_rec.tenant_id AND organization_id = org_rec.organization_id
          AND code = '1100' AND deleted_at IS NULL;

        SELECT id INTO acct_1200_id FROM accounts
        WHERE tenant_id = org_rec.tenant_id AND organization_id = org_rec.organization_id
          AND code = '1200' AND deleted_at IS NULL;

        -- Step 1: Update bank_accounts table - repoint from 1100 to 1010
        IF acct_1010_id IS NOT NULL AND acct_1100_id IS NOT NULL THEN
            UPDATE bank_accounts
            SET account_id = acct_1010_id, updated_at = v_now
            WHERE account_id = acct_1100_id;
        END IF;

        -- Step 2: Rename 1010 from "Petty Cash" to "Bank Account"
        IF acct_1010_id IS NOT NULL THEN
            UPDATE accounts SET
                name = 'Bank Account',
                name_uz = 'Bank hisobi',
                name_en = 'Bank Account',
                description = 'Main bank account',
                is_bank_account = true,
                is_reconcilable = true,
                updated_at = v_now
            WHERE id = acct_1010_id;
        END IF;

        -- Step 3: Rename 1100 from "Bank Account" to "Accounts Receivable"
        IF acct_1100_id IS NOT NULL THEN
            UPDATE accounts SET
                name = 'Accounts Receivable',
                name_uz = 'Debitorlar',
                name_en = 'Accounts Receivable',
                description = 'Trade receivables from customers',
                account_type_id = ar_type_id,
                is_bank_account = false,
                is_control_account = true,
                is_reconcilable = true,
                updated_at = v_now
            WHERE id = acct_1100_id;
        END IF;

        -- Step 4: Transfer 1200 journal entries to 1100, then soft-delete 1200
        IF acct_1200_id IS NOT NULL AND acct_1100_id IS NOT NULL THEN
            -- Reassign journal entry lines
            UPDATE journal_entry_lines
            SET account_id = acct_1100_id
            WHERE account_id = acct_1200_id;

            -- Transfer balance
            UPDATE accounts
            SET current_balance = current_balance + (
                SELECT COALESCE(current_balance, 0) FROM accounts WHERE id = acct_1200_id
            ), updated_at = v_now
            WHERE id = acct_1100_id;

            -- Soft-delete 1200
            UPDATE accounts
            SET deleted_at = v_now, is_active = false, current_balance = 0, updated_at = v_now
            WHERE id = acct_1200_id;
        END IF;

        -- Step 5: Update journal default accounts (SAL journal: 1200→1100, BANK journal: 1100→1010)
        IF acct_1100_id IS NOT NULL THEN
            UPDATE journals
            SET default_debit_account_id = acct_1100_id, updated_at = v_now
            WHERE tenant_id = org_rec.tenant_id
              AND default_debit_account_id = acct_1200_id;
        END IF;

        IF acct_1010_id IS NOT NULL THEN
            UPDATE journals
            SET default_debit_account_id = acct_1010_id, updated_at = v_now
            WHERE tenant_id = org_rec.tenant_id
              AND default_debit_account_id = acct_1100_id
              AND type = 'bank';

            UPDATE journals
            SET default_credit_account_id = acct_1010_id, updated_at = v_now
            WHERE tenant_id = org_rec.tenant_id
              AND default_credit_account_id = acct_1100_id
              AND type = 'bank';
        END IF;
    END LOOP;
END $$;
