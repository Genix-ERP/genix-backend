-- 385_journal_enrichment_use_contracts_table.sql
--
-- Final fix in the journal_entry_lines analytics enrichment chain.
--
-- Background of this saga (for the next person who has to debug §4.5):
--   * Migration 325 — original enrichment trigger.
--   * Migration 357 — added vendor-level fallback to procurement_contracts.
--   * Migration 383 — added bill→PO→contract walk for purchase invoices.
--   * Migration 384 — fixed `NEW.tenant_id` ref (column doesn't exist on
--                     journal_entry_lines; tenant scope inherited from
--                     journal_entries via FK).
--   * Migration 385 (this one) — fixes the WRONG TABLE bug: the
--                     vendor-level fallback queried `procurement_contracts`,
--                     but `journal_entry_lines.contract_id` has a foreign
--                     key constraint `fk_jel_contract` referencing
--                     `contracts(id)` (set up in migration 318), NOT
--                     procurement_contracts(id).
--
-- Symptom: trigger correctly finds a UUID from procurement_contracts, sets
-- NEW.contract_id, then the FK constraint immediately rejects:
--   pq: insert or update on table "journal_entry_lines"
--       violates foreign key constraint "fk_jel_contract"
--
-- Fix: look up `contracts` (the FK target) using its supplier_id column
-- instead of procurement_contracts.vendor_id. The companion code change
-- in CreateBillFromPO (purchase_orders.go) also writes spot-purchase
-- contracts into `contracts` so the trigger has something to find for
-- vendors that never had a master agreement.
--
-- Two-table-confusion note: the `contracts` and `procurement_contracts`
-- tables coexist in this codebase from different historical migrations
-- (012 vs 017/054). They are NOT linked. The journal-line FK chose
-- `contracts` and that's what we standardize on for analytics.
-- Tenants that only populate `procurement_contracts` will still see
-- §4.5 errors unless they also have a corresponding `contracts` row;
-- the spot-purchase auto-create in purchase_orders.go covers that for
-- the bill-from-PO flow specifically.
--
-- Idempotent: pure CREATE OR REPLACE FUNCTION.

CREATE OR REPLACE FUNCTION fn_auto_enrich_journal_line_analytics()
RETURNS TRIGGER AS $$
DECLARE
    v_account_code      TEXT;
    v_mandatory         BOOLEAN;
    v_analytics_types   JSONB;
    v_source_type       TEXT;
    v_source_id         UUID;
    v_tenant_id         UUID;
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

    SELECT source_type, source_id, tenant_id
      INTO v_source_type, v_source_id, v_tenant_id
    FROM journal_entries WHERE id = NEW.journal_entry_id;

    IF v_source_type IS NULL OR v_source_id IS NULL THEN
        RETURN NEW;
    END IF;

    -- contact_id enrichment (unchanged)
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

    -- contract_id enrichment chain. Lookup the FK target table
    -- (contracts), NOT procurement_contracts.
    IF NEW.contract_id IS NULL AND v_analytics_types ? 'shartnoma' THEN
        -- Step 1: direct column on the source document. These columns
        -- (sales_invoices.contract_id / purchase_invoices.contract_id)
        -- already reference contracts(id) where they exist; if a column
        -- doesn't exist the EXCEPTION block leaves NEW.contract_id NULL.
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

        -- Step 2: bill→PO→contract walk for purchase invoices
        -- (CreateBillFromPO flow). purchase_orders.contract_id should
        -- itself reference contracts(id) by convention; if the row in
        -- contracts doesn't exist anymore, the FK below catches it.
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

        -- Step 3 (last resort): vendor-level master contract from the
        -- `contracts` table. Use supplier_id (the column name on
        -- contracts) — procurement_contracts.vendor_id is a different
        -- table. Filter by tenant_id from journal_entries (NOT the
        -- non-existent NEW.tenant_id).
        IF NEW.contract_id IS NULL AND NEW.contact_id IS NOT NULL THEN
            BEGIN
                SELECT id INTO v_tmp_uuid
                FROM contracts
                WHERE tenant_id   = v_tenant_id
                  AND supplier_id = NEW.contact_id
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
    'TT §4.5: auto-fill analytics on journal_entry_lines. Vendor-level '
    'contract fallback queries `contracts` (FK target) by supplier_id, '
    'NOT procurement_contracts. tenant_id is read from the parent '
    'journal_entries row since journal_entry_lines has no tenant_id '
    'column. (Updated in migration 385.)';
