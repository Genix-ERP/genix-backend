-- 496_journal_enrichment_warehouse_for_sales_invoice.sql
--
-- Fixes: "Hisob-faktura yaratib bo'lmadi" (500) on Savdo buyurtmalari →
-- hisob-faktura yaratish.
--
--   pq: TT §4.5: account 2910 (Sotib olingan tovarlar) requires warehouse
--       (ombor); line source_type=sales_invoice
--
-- Root cause: fn_auto_enrich_journal_line_analytics() tried to fill
-- warehouse_id for a sales_invoice with
--     SELECT warehouse_id FROM sales_invoices WHERE id = ...
-- but sales_invoices has NO warehouse_id column. The lookup raised
-- undefined_column, the handler-level EXCEPTION swallowed it, warehouse_id
-- stayed NULL, and the §4.5 invariant then rejected the COGS/stock-output
-- line — aborting the whole CreateInvoiceFromOrder transaction. So invoicing
-- a shipped order has never worked on tenants whose stock accounts carry
-- mandatory 'ombor' analytics.
--
-- Fix: resolve the warehouse the same way migration 393 did for
-- purchase_invoice, but walking the SALES side:
--   1. direct column (kept, in case a deployment adds one later)
--   2. invoice → sales_order → stock_operations(direction='delivery')
--      → source_location → warehouse
--   3. invoice → sales_order → sales_delivery_orders.warehouse_id
--   4. the tenant's single warehouse (only when exactly one exists)
--   5. otherwise NULL — §4.5 still rejects rather than inventing an ombor.
--
-- Based on the CURRENT function body (migration 443), so none of the
-- contract/contact/employee enrichment added since 393 is reverted.
--
-- Companion handler change (sales.go CreateInvoiceFromOrder): the COGS and
-- stock-output lines now pass warehouse_id explicitly and abort the request
-- with a clear message instead of logging and continuing into a poisoned
-- transaction.
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
    IF NEW.warehouse_id IS NULL AND v_analytics_types ? 'ombor' THEN
        CASE
            WHEN v_source_type IN ('sales_invoice', 'invoice') THEN
                BEGIN
                    -- Step 1: direct column, if this deployment has one.
                    -- sales_invoices has NO warehouse_id column, so this
                    -- raises undefined_column and is swallowed — which is
                    -- exactly why enrichment silently produced NULL and
                    -- §4.5 rejected every COGS line (migration 496).
                    BEGIN
                        SELECT warehouse_id INTO v_tmp_uuid
                        FROM sales_invoices WHERE id = v_source_id;
                    EXCEPTION WHEN undefined_table OR undefined_column THEN
                        v_tmp_uuid := NULL;
                    END;

                    -- Step 2: walk invoice -> sales order -> delivery stock
                    -- operation -> source location -> warehouse.
                    IF v_tmp_uuid IS NULL THEN
                        BEGIN
                            SELECT wl.warehouse_id INTO v_tmp_uuid
                            FROM sales_invoices si
                            JOIN stock_operations so ON so.source_id = si.sales_order_id
                            LEFT JOIN warehouse_locations wl ON wl.id = so.source_location_id
                            WHERE si.id = v_source_id
                              AND so.source_type = 'sales_order'
                              AND so.direction = 'delivery'
                              AND so.deleted_at IS NULL
                              AND so.state <> 'cancelled'
                              AND wl.warehouse_id IS NOT NULL
                            ORDER BY so.created_at DESC
                            LIMIT 1;
                        EXCEPTION WHEN undefined_table OR undefined_column THEN
                            v_tmp_uuid := NULL;
                        END;
                    END IF;

                    -- Step 3: delivery order on the same sales order.
                    IF v_tmp_uuid IS NULL THEN
                        BEGIN
                            SELECT do2.warehouse_id INTO v_tmp_uuid
                            FROM sales_invoices si
                            JOIN sales_delivery_orders do2 ON do2.sales_order_id = si.sales_order_id
                            WHERE si.id = v_source_id
                              AND do2.warehouse_id IS NOT NULL
                            ORDER BY do2.created_at DESC
                            LIMIT 1;
                        EXCEPTION WHEN undefined_table OR undefined_column THEN
                            v_tmp_uuid := NULL;
                        END;
                    END IF;

                    -- Step 4: the tenant's only/default warehouse. Safe here
                    -- because a single-warehouse tenant has no ambiguity; with
                    -- several we leave NULL and let §4.5 reject rather than
                    -- invent an ombor.
                    IF v_tmp_uuid IS NULL THEN
                        BEGIN
                            SELECT w.id INTO v_tmp_uuid
                            FROM warehouses w
                            JOIN journal_entries je2 ON je2.id = NEW.journal_entry_id
                            WHERE w.tenant_id = je2.tenant_id
                              AND w.deleted_at IS NULL
                              AND COALESCE(w.is_active, true) = true
                            HAVING COUNT(*) OVER () = 1
                            LIMIT 1;
                        EXCEPTION WHEN undefined_table OR undefined_column THEN
                            v_tmp_uuid := NULL;
                        END;
                    END IF;

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

            WHEN v_source_type IN ('purchase_invoice', 'vendor_bill') THEN
                BEGIN
                    SELECT gr.warehouse_id INTO v_tmp_uuid
                    FROM purchase_invoices pi
                    LEFT JOIN goods_receipts gr ON gr.id = pi.goods_receipt_id
                    WHERE pi.id = v_source_id;

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
    -- Migration 443: fallback now walks procurement_contracts (the FK
    -- target after 443 §9) with the tenant predicate restored.
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

        -- Step 3: counterparty-level fallback via procurement_contracts.
        IF NEW.contract_id IS NULL AND NEW.contact_id IS NOT NULL THEN
            BEGIN
                SELECT id INTO v_tmp_uuid
                FROM procurement_contracts
                WHERE tenant_id = v_tenant_id
                  AND vendor_id = NEW.contact_id
                  AND COALESCE(status, 'active') IN ('active', 'draft', 'negotiation', 'signing')
                  AND deleted_at IS NULL
                  AND archived_at IS NULL
                ORDER BY created_at DESC
                LIMIT 1;
                NEW.contract_id := v_tmp_uuid;
            EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
