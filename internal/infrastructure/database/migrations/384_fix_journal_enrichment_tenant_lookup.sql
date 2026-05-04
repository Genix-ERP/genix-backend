-- 384_fix_journal_enrichment_tenant_lookup.sql
--
-- Fixes a long-standing latent bug in the journal_entry_lines analytics
-- enrichment trigger (migrations 325 → 357 → 383): the vendor-level
-- contract fallback filters by `NEW.tenant_id`, but `journal_entry_lines`
-- has no `tenant_id` column (its tenant scope is inherited from the
-- parent `journal_entries` row via FK). Inside the trigger function,
-- `NEW.tenant_id` raises an `undefined_column` exception that the
-- existing `EXCEPTION WHEN undefined_table OR undefined_column` block
-- silently swallows — so the fallback always returns NULL, even when
-- a perfectly valid procurement_contract exists for the vendor in the
-- correct tenant.
--
-- Symptom: even after migration 383's bill→PO→contract walk and the
-- code-side spot-contract auto-create in CreateBillFromPO, the JE
-- still 500s with:
--    pq: TT §4.5: account 6010 ... requires contract (shartnoma);
--        line source_type=purchase_invoice
--
-- Fix: at the top of the function, fetch the tenant_id from
-- `journal_entries` along with source_type/source_id (single existing
-- query, just adds one column), then use that variable wherever the
-- old code wrote `NEW.tenant_id`.
--
-- Idempotent: pure CREATE OR REPLACE FUNCTION, no schema changes.

CREATE OR REPLACE FUNCTION fn_auto_enrich_journal_line_analytics()
RETURNS TRIGGER AS $$
DECLARE
    v_account_code      TEXT;
    v_mandatory         BOOLEAN;
    v_analytics_types   JSONB;
    v_source_type       TEXT;
    v_source_id         UUID;
    v_tenant_id         UUID;   -- fetched from parent journal_entries
    v_tmp_uuid          UUID;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RETURN NEW;
    END IF;

    SELECT code, COALESCE(mandatory_analytics, false),
           COALESCE(analytics_types, '[]'::jsonb)
      INTO v_account_code, v_mandatory, v_analytics_types
    FROM accounts
    WHERE id = NEW.account_id;

    IF NOT v_mandatory THEN
        RETURN NEW;
    END IF;

    -- Pull source ref + tenant from the parent journal entry. tenant_id
    -- on journal_entries is the canonical tenant scope; journal_entry_lines
    -- has no tenant_id column of its own.
    SELECT source_type, source_id, tenant_id
      INTO v_source_type, v_source_id, v_tenant_id
    FROM journal_entries WHERE id = NEW.journal_entry_id;

    IF v_source_type IS NULL OR v_source_id IS NULL THEN
        RETURN NEW;
    END IF;

    -- contact_id enrichment (unchanged from 325/357/383)
    IF NEW.contact_id IS NULL AND v_analytics_types ? 'kontragent' THEN
        CASE
            WHEN v_source_type IN ('sales_invoice', 'invoice') THEN
                BEGIN
                    SELECT customer_id INTO v_tmp_uuid
                    FROM sales_invoices WHERE id = v_source_id;
                    NEW.contact_id := v_tmp_uuid;
                EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;

            WHEN v_source_type = 'sales_return' THEN
                BEGIN
                    SELECT customer_id INTO v_tmp_uuid
                    FROM sales_returns WHERE id = v_source_id;
                    NEW.contact_id := v_tmp_uuid;
                EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;

            WHEN v_source_type IN ('purchase_invoice', 'vendor_bill') THEN
                BEGIN
                    SELECT COALESCE(supplier_id, vendor_id) INTO v_tmp_uuid
                    FROM purchase_invoices WHERE id = v_source_id;
                    NEW.contact_id := v_tmp_uuid;
                EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;

            WHEN v_source_type = 'purchase_return' THEN
                BEGIN
                    SELECT COALESCE(supplier_id, vendor_id) INTO v_tmp_uuid
                    FROM purchase_returns WHERE id = v_source_id;
                    NEW.contact_id := v_tmp_uuid;
                EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;

            ELSE
                NULL;
        END CASE;
    END IF;

    -- warehouse_id enrichment (unchanged)
    IF NEW.warehouse_id IS NULL AND v_analytics_types ? 'ombor' THEN
        CASE
            WHEN v_source_type IN ('sales_invoice', 'invoice') THEN
                BEGIN
                    SELECT warehouse_id INTO v_tmp_uuid
                    FROM sales_invoices WHERE id = v_source_id;
                    NEW.warehouse_id := v_tmp_uuid;
                EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;

            WHEN v_source_type = 'sales_return' THEN
                BEGIN
                    SELECT warehouse_id INTO v_tmp_uuid
                    FROM sales_returns WHERE id = v_source_id;
                    NEW.warehouse_id := v_tmp_uuid;
                EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;

            WHEN v_source_type = 'purchase_return' THEN
                BEGIN
                    SELECT warehouse_id INTO v_tmp_uuid
                    FROM purchase_returns WHERE id = v_source_id;
                    NEW.warehouse_id := v_tmp_uuid;
                EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;

            WHEN v_source_type IN ('work_order', 'production_order') THEN
                BEGIN
                    SELECT warehouse_id INTO v_tmp_uuid
                    FROM work_orders WHERE id = v_source_id;
                    NEW.warehouse_id := v_tmp_uuid;
                EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;

            ELSE
                NULL;
        END CASE;
    END IF;

    -- employee_id enrichment (unchanged)
    IF NEW.employee_id IS NULL AND v_analytics_types ? 'xodim' THEN
        CASE
            WHEN v_source_type IN ('payroll', 'payroll_entry', 'payroll_payment', 'salary_deduction') THEN
                BEGIN
                    SELECT employee_id INTO v_tmp_uuid
                    FROM payroll_entries WHERE id = v_source_id;
                    NEW.employee_id := v_tmp_uuid;
                EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;

            WHEN v_source_type = 'employee_loan' THEN
                BEGIN
                    SELECT employee_id INTO v_tmp_uuid
                    FROM employee_loans WHERE id = v_source_id;
                    NEW.employee_id := v_tmp_uuid;
                EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;

            WHEN v_source_type = 'expense' THEN
                BEGIN
                    SELECT employee_id INTO v_tmp_uuid
                    FROM expenses WHERE id = v_source_id;
                    NEW.employee_id := v_tmp_uuid;
                EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;

            ELSE
                NULL;
        END CASE;
    END IF;

    -- contract_id enrichment with full fallback chain. Uses v_tenant_id
    -- (fetched above from journal_entries) instead of the broken
    -- NEW.tenant_id reference that previous versions of this trigger
    -- silently swallowed via the exception handler.
    IF NEW.contract_id IS NULL AND v_analytics_types ? 'shartnoma' THEN
        -- Step 1: direct column lookup on the source document
        CASE
            WHEN v_source_type IN ('sales_invoice', 'invoice') THEN
                BEGIN
                    SELECT contract_id INTO v_tmp_uuid
                    FROM sales_invoices WHERE id = v_source_id;
                    NEW.contract_id := v_tmp_uuid;
                EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;

            WHEN v_source_type IN ('purchase_invoice', 'vendor_bill') THEN
                BEGIN
                    SELECT contract_id INTO v_tmp_uuid
                    FROM purchase_invoices WHERE id = v_source_id;
                    NEW.contract_id := v_tmp_uuid;
                EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;

            WHEN v_source_type = 'goods_receipt' THEN
                BEGIN
                    SELECT po.contract_id INTO v_tmp_uuid
                    FROM goods_receipts gr
                    LEFT JOIN purchase_orders po ON po.id = gr.purchase_order_id
                    WHERE gr.id = v_source_id;
                    NEW.contract_id := v_tmp_uuid;
                EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;

            ELSE
                NULL;
        END CASE;

        -- Step 2: bill→PO→contract walk for purchase invoices created
        -- from POs (the CreateBillFromPO flow). Same as migration 383.
        IF NEW.contract_id IS NULL
           AND v_source_type IN ('purchase_invoice', 'vendor_bill') THEN
            BEGIN
                SELECT po.contract_id INTO v_tmp_uuid
                FROM purchase_invoices pi
                JOIN purchase_orders   po ON po.id = pi.purchase_order_id
                WHERE pi.id = v_source_id;
                NEW.contract_id := v_tmp_uuid;
            EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;
        END IF;

        -- Step 3 (last resort): vendor-level master contract.
        -- THIS is the line that was silently failing — it used
        -- NEW.tenant_id which doesn't exist on journal_entry_lines.
        -- Now uses v_tenant_id from journal_entries.
        IF NEW.contract_id IS NULL AND NEW.contact_id IS NOT NULL THEN
            BEGIN
                SELECT id INTO v_tmp_uuid
                FROM procurement_contracts
                WHERE tenant_id = v_tenant_id
                  AND vendor_id = NEW.contact_id
                  AND COALESCE(status, 'active') IN ('active', 'draft', 'approved')
                  AND deleted_at IS NULL
                ORDER BY created_at DESC
                LIMIT 1;
                NEW.contract_id := v_tmp_uuid;
            EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION fn_auto_enrich_journal_line_analytics() IS
    'TT §4.5: auto-fill analytics on journal_entry_lines. Looks up '
    'tenant_id from the parent journal_entries row (journal_entry_lines '
    'has no tenant_id column of its own — earlier versions of this '
    'trigger broke silently on this). Walks bill→PO→contract before '
    'falling back to the vendor-level master procurement_contract. '
    '(Updated in migration 384.)';
