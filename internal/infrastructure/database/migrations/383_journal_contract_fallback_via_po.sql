-- 383_journal_contract_fallback_via_po.sql
--
-- Adds one more fallback step to the journal_entry_lines analytics
-- enrichment trigger introduced in migration 325 and extended in
-- migration 357 (TT §4.5 / shartnoma propagation).
--
-- Problem
-- ───────
-- When a vendor bill is auto-generated from a purchase order
-- ("Create Bill from PO" flow in purchase_orders.go), the bill is
-- written into `purchase_invoices` but its source PO's contract is
-- never copied — partly because `purchase_invoices` doesn't even
-- carry a `contract_id` column. The CreateBillFromPO handler then
-- inserts a credit line on account 6010 (Mol yetkazib beruvchilar
-- va pudratchilar). The §4.5 enforcement trigger requires
-- `contract_id`. The enrichment trigger from migration 325/357
-- looks up `purchase_invoices.contract_id` (which doesn't exist as
-- a column → caught by EXCEPTION → NULL), then falls back to the
-- most recent active procurement_contract for the vendor. If no
-- such contract exists for the vendor, the line is rejected:
--
--   pq: TT §4.5: account 6010 (Mol yetkazib beruvchilar va
--   pudratchilar) requires contract (shartnoma);
--   line source_type=purchase_invoice
--
-- Many tenants have a contract on the PO itself but no
-- vendor-level master contract, so the existing fallback misses
-- them.
--
-- Fix
-- ───
-- Insert one extra fallback BEFORE the vendor-level lookup: when
-- the source is a purchase_invoice, walk
--    purchase_invoices.purchase_order_id → purchase_orders.contract_id
-- and use that. This unblocks bills created from POs that have a
-- contract attached, without forcing us to add a new column or
-- modify every CreateBillFromPO call site.
--
-- Behaviour preserved
-- ───────────────────
-- * Direct read of `purchase_invoices.contract_id` is kept so a
--   future column addition will Just Work.
-- * Vendor-level fallback (procurement_contracts WHERE vendor_id =
--   contact_id ORDER BY created_at DESC) is kept as the final net.
-- * Sales side, goods_receipt path, kontragent/employee/warehouse
--   enrichment — all unchanged.
--
-- Idempotent: this is a `CREATE OR REPLACE FUNCTION`, no schema
-- changes, safe to re-run.

CREATE OR REPLACE FUNCTION fn_auto_enrich_journal_line_analytics()
RETURNS TRIGGER AS $$
DECLARE
    v_account_code      TEXT;
    v_mandatory         BOOLEAN;
    v_analytics_types   JSONB;
    v_source_type       TEXT;
    v_source_id         UUID;
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

    SELECT source_type, source_id INTO v_source_type, v_source_id
    FROM journal_entries WHERE id = NEW.journal_entry_id;

    IF v_source_type IS NULL OR v_source_id IS NULL THEN
        RETURN NEW;
    END IF;

    -- contact_id enrichment (unchanged from migrations 325/357)
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

    -- contract_id enrichment with PO-walk + vendor fallback chain.
    -- Order:
    --   1. Direct column on the source document
    --      (sales_invoices.contract_id / purchase_invoices.contract_id)
    --   2. **NEW (this migration):** for purchase_invoices, walk
    --      purchase_invoices.purchase_order_id → purchase_orders.contract_id
    --   3. For goods_receipt: walk goods_receipts.purchase_order_id
    --   4. Vendor-level fallback: most recent active procurement_contract
    --      for the contact_id we already enriched above.
    IF NEW.contract_id IS NULL AND v_analytics_types ? 'shartnoma' THEN
        -- Step 1: direct column lookup (existing behaviour)
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

        -- Step 2 (NEW): for purchase_invoices, when step 1 didn't find
        -- a contract on the bill itself, walk through the linked
        -- purchase_order to grab its contract. This is the case the
        -- "Create Bill from PO" flow falls into — the PO carries the
        -- contract; the bill is just a derived document.
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

        -- Step 4 (last resort, unchanged): vendor-level master
        -- contract. Already in migration 357; kept here so the chain
        -- ends consistently for tenants where the PO itself had no
        -- contract attached but the vendor has a master agreement.
        IF NEW.contract_id IS NULL AND NEW.contact_id IS NOT NULL THEN
            BEGIN
                SELECT id INTO v_tmp_uuid
                FROM procurement_contracts
                WHERE tenant_id = NEW.tenant_id
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
    'TT §4.5: auto-fill analytics on journal_entry_lines. '
    'For purchase_invoices, walks bill→PO→contract before falling back '
    'to the vendor-level master procurement_contract. (Updated in migration 383.)';
