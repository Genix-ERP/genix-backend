-- 393_journal_enrichment_warehouse_for_purchase_invoice.sql
--
-- Extends fn_auto_enrich_journal_line_analytics() so it knows how to
-- fill in warehouse_id when the JE source is a purchase_invoice.
--
-- Why this exists:
--   TT §4.5 enforces "ombor" analytics on every inventory account
--   (1010, 1030, 1050, 1060, 1090 — Xom ashyo, Yoqilg'i, etc).
--   migration 390's trigger handles warehouse_id enrichment for
--   sales_invoice / sales_return / purchase_return / work_order, but
--   NOT for purchase_invoice — even though the bill-from-PO flow
--   debits a stock-input account that requires warehouse_id.
--
--   Symptom seen on prod (CreateBillFromPO):
--     pq: TT §4.5: account 1030 (Yoqilg'i) requires warehouse (ombor);
--                  line source_type=purchase_invoice
--
-- Lookup chain for purchase_invoice → warehouse:
--   1. purchase_invoices.goods_receipt_id → goods_receipts.warehouse_id
--      (preferred — direct FK link, set when the bill was created from
--      a 3-way match against a goods receipt).
--   2. purchase_invoices.purchase_order_id → stock_operations
--      (direction='receipt') → warehouse_locations.warehouse_id
--      (fallback — most flows go via stock_operations not goods_receipts).
--   3. NULL — let the §4.5 trigger reject if no warehouse can be inferred.
--      The handler is expected to fix the data and retry; we don't want
--      to silently invent a warehouse.
--
-- Companion code change in handler/purchase_orders.go: CreateBillFromPO
-- now also packs warehouse_id explicitly per JE line so the trigger
-- enrichment is a belt-and-suspenders backstop, not the only path.
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

    SELECT source_type, source_id INTO v_source_type, v_source_id
    FROM journal_entries WHERE id = NEW.journal_entry_id;

    IF v_source_type IS NULL OR v_source_id IS NULL THEN
        RETURN NEW;
    END IF;

    -- ── contact_id enrichment (kontragent) ────────────────────────────────
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

            WHEN v_source_type = 'goods_receipt' THEN
                BEGIN
                    SELECT supplier_id INTO v_tmp_uuid
                    FROM goods_receipts WHERE id = v_source_id;
                    NEW.contact_id := v_tmp_uuid;
                EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;

            WHEN v_source_type IN ('payment', 'payment_receipt') THEN
                BEGIN
                    SELECT contact_id INTO v_tmp_uuid
                    FROM payments WHERE id = v_source_id;
                    NEW.contact_id := v_tmp_uuid;
                EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;

            WHEN v_source_type = 'expense' THEN
                BEGIN
                    SELECT vendor_id INTO v_tmp_uuid
                    FROM expenses WHERE id = v_source_id;
                    NEW.contact_id := v_tmp_uuid;
                EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;

            WHEN v_source_type = 'landed_cost' THEN
                BEGIN
                    SELECT vendor_id INTO v_tmp_uuid
                    FROM landed_costs WHERE id = v_source_id;
                    NEW.contact_id := v_tmp_uuid;
                EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;

            ELSE
                NULL;
        END CASE;
    END IF;

    -- ── warehouse_id enrichment (ombor) ──────────────────────────────────
    -- Adds purchase_invoice case (NEW in migration 393).
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

            WHEN v_source_type = 'goods_receipt' THEN
                BEGIN
                    SELECT warehouse_id INTO v_tmp_uuid
                    FROM goods_receipts WHERE id = v_source_id;
                    NEW.warehouse_id := v_tmp_uuid;
                EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;

            -- NEW in migration 393.
            WHEN v_source_type IN ('purchase_invoice', 'vendor_bill') THEN
                BEGIN
                    -- Step 1: direct goods_receipt link (3-way matched bills).
                    SELECT gr.warehouse_id INTO v_tmp_uuid
                    FROM purchase_invoices pi
                    LEFT JOIN goods_receipts gr ON gr.id = pi.goods_receipt_id
                    WHERE pi.id = v_source_id;

                    -- Step 2: walk PO → most-recent receipt operation
                    -- → dest_location → warehouse. Most bill flows go
                    -- through stock_operations rather than goods_receipts.
                    IF v_tmp_uuid IS NULL THEN
                        SELECT wl.warehouse_id INTO v_tmp_uuid
                        FROM purchase_invoices pi
                        JOIN stock_operations so ON so.source_id = pi.purchase_order_id
                        LEFT JOIN warehouse_locations wl ON wl.id = so.dest_location_id
                        WHERE pi.id = v_source_id
                          AND so.source_type = 'purchase_order'
                          AND so.direction = 'receipt'
                          AND so.deleted_at IS NULL
                          AND so.state != 'cancelled'
                          AND wl.warehouse_id IS NOT NULL
                        ORDER BY so.created_at DESC
                        LIMIT 1;
                    END IF;

                    NEW.warehouse_id := v_tmp_uuid;
                EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;

            ELSE
                NULL;
        END CASE;
    END IF;

    -- ── employee_id enrichment (xodim) ────────────────────────────────────
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

    -- ── contract_id enrichment (shartnoma) ────────────────────────────────
    -- Walks contracts (NOT procurement_contracts — see migration 390).
    IF NEW.contract_id IS NULL AND v_analytics_types ? 'shartnoma' THEN
        -- Step 1: direct column on the source document.
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

            ELSE
                NULL;
        END CASE;

        -- Step 2: walk via PO if bill has no direct contract.
        IF NEW.contract_id IS NULL AND v_source_type IN ('purchase_invoice', 'vendor_bill') THEN
            BEGIN
                SELECT po.contract_id INTO v_tmp_uuid
                FROM purchase_invoices pi
                JOIN purchase_orders po ON po.id = pi.purchase_order_id
                WHERE pi.id = v_source_id;
                NEW.contract_id := v_tmp_uuid;
            EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;
        END IF;

        -- Step 3: vendor-level fallback via contracts table (FK target).
        IF NEW.contract_id IS NULL AND NEW.contact_id IS NOT NULL THEN
            BEGIN
                SELECT id INTO v_tmp_uuid
                FROM contracts
                WHERE supplier_id = NEW.contact_id
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
    'TT §4.5 auto-fill for journal_entry_lines analytics. Migration 393 '
    'added warehouse_id enrichment for purchase_invoice via PO → '
    'stock_operations → dest_location → warehouse.';
