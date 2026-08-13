-- 497_journal_enrichment_warehouse_for_production.sql
--
-- Fixes: 500 on POST /manufacturing/production-orders/:id/start on tenants
-- whose stock accounts carry mandatory 'ombor' analytics (migration 317
-- marks 1010..1090 and 2810/2910 with analytics_types=["ombor"],
-- mandatory_analytics=true; migration 326 made the check a hard EXCEPTION).
--
--   pq: TT §4.5: account 1010 (Xom ashyo va materiallar) requires warehouse
--       (ombor); line source_type=production_start
--
-- Root cause (same class as 393/496): fn_auto_enrich_journal_line_analytics
-- has NO warehouse case for the production JE source types
-- (production_start / production_complete / production_return /
-- production_cancel / production_order_consume / split_output_complete),
-- and its existing 'work_order'/'production_order' case reads
--     SELECT warehouse_id FROM work_orders WHERE id = ...
-- but work_orders has NO warehouse_id column — undefined_column was
-- silently swallowed, so that branch has never filled anything. The
-- manufacturing handlers insert JE lines without warehouse_id, enrichment
-- leaves it NULL, and §4.5 aborts the whole start/complete transaction.
--
-- Fix, based on the CURRENT function body (migration 496):
--   1. All production source types resolve the warehouse from
--      production_orders.warehouse_id (source_id = production order UUID for
--      every production JE — dedupe keys in manufacturing.go /
--      production_split.go / work_orders.go). StartProductionOrder persists
--      its auto-assigned warehouse in the same tx BEFORE the JE lines, so
--      the lookup sees it.
--   2. 'work_order' walks work_orders → production_orders.
--   3. Shared fallback: the tenant's only active warehouse (single-warehouse
--      tenants have no ambiguity; with several we leave NULL and let §4.5
--      reject rather than invent an ombor).
--   4. Repairs 496's sales_invoice step-4 fallback: it used
--      "HAVING COUNT(*) OVER () = 1" — window functions are not allowed in
--      HAVING (42P20), and 42P20 is NOT caught by the undefined_table/
--      undefined_column handlers, so any single-warehouse fallback attempt
--      aborted the invoicing transaction. Rewritten as a plain aggregate:
--      SELECT MIN(id::text)::uuid ... HAVING COUNT(*) = 1.
--
-- Companion handler change (manufacturing.go StartProductionOrder): the
-- start-JE lines now pass warehouse_id explicitly and a rejected line
-- surfaces the trigger's message as a 422 instead of an opaque 500.
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
                    -- raises undefined_column and is swallowed (496).
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

                    -- Step 4: the tenant's only warehouse. 496 wrote this
                    -- with a window function in HAVING (42P20 — always
                    -- raised when reached, and NOT caught below); plain
                    -- aggregate now.
                    IF v_tmp_uuid IS NULL THEN
                        BEGIN
                            SELECT MIN(w.id::text)::uuid INTO v_tmp_uuid
                            FROM warehouses w
                            JOIN journal_entries je2 ON je2.id = NEW.journal_entry_id
                            WHERE w.tenant_id = je2.tenant_id
                              AND w.deleted_at IS NULL
                              AND COALESCE(w.is_active, true) = true
                            HAVING COUNT(*) = 1;
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

            -- Production JEs: source_id is ALWAYS the production order UUID
            -- (manufacturing.go dedupe keys for production_start/_complete/
            -- _return/_cancel; work_orders.go production_order_consume;
            -- production_split.go split_output_complete). 'production_order'
            -- kept for legacy entries — 496 wrongly read work_orders (which
            -- has no warehouse_id column), so it never resolved anything.
            WHEN v_source_type IN ('production_start', 'production_complete',
                                   'production_return', 'production_cancel',
                                   'production_order_consume',
                                   'split_output_complete',
                                   'production_order') THEN
                BEGIN
                    SELECT warehouse_id INTO v_tmp_uuid
                    FROM production_orders WHERE id = v_source_id;

                    -- Fallback: the tenant's only active warehouse.
                    IF v_tmp_uuid IS NULL THEN
                        SELECT MIN(w.id::text)::uuid INTO v_tmp_uuid
                        FROM warehouses w
                        JOIN journal_entries je2 ON je2.id = NEW.journal_entry_id
                        WHERE w.tenant_id = je2.tenant_id
                          AND w.deleted_at IS NULL
                          AND COALESCE(w.is_active, true) = true
                        HAVING COUNT(*) = 1;
                    END IF;

                    NEW.warehouse_id := v_tmp_uuid;
                EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;

            WHEN v_source_type = 'work_order' THEN
                BEGIN
                    SELECT po.warehouse_id INTO v_tmp_uuid
                    FROM work_orders wo
                    JOIN production_orders po ON po.id = wo.production_order_id
                    WHERE wo.id = v_source_id;
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
