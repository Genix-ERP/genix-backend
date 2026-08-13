-- 501_journal_enrichment_for_cash_and_vipiska.sql
--
-- Pul oqimi deep fix (2026-08-13 audit): fn_auto_enrich_journal_line_analytics
-- had NO kontragent case for source_type='cash_order' (kassa PKO/RKO confirm,
-- finance_extra.go) or 'bank_vipiska' (vipiska line confirm,
-- buxgalteriya_vipiska_import.go). On tenants whose chart carries migration
-- 317's mandatory_analytics flags, confirming a kassa order or a vipiska line
-- against 6010/4010 (kontragent + shartnoma) raised TT SS4.5 and surfaced as an
-- opaque 500 whenever the handler had no partner to pass.
--
-- Two WHEN branches are added to the kontragent section:
--   * cash_order    -> cash_orders.partner_id       (source_id = order UUID)
--   * bank_vipiska  -> bank_statement_transactions.matched_contact_id
--                                                   (source_id = line UUID)
-- With the contact filled, the existing shartnoma Step-3 counterparty fallback
-- (procurement_contracts by vendor) can also resolve the contract dimension.
-- xodim/ombor stay unenriched for these sources on purpose: neither document
-- carries an employee or a warehouse, and inventing one would be worse than
-- the (now friendly, handler-side) SS4.5 rejection.
--
-- Everything else is the CURRENT function body from 497, unchanged.
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

            WHEN v_source_type = 'cash_order' THEN
                BEGIN
                    SELECT partner_id INTO v_tmp_uuid
                    FROM cash_orders WHERE id = v_source_id;
                    NEW.contact_id := v_tmp_uuid;
                EXCEPTION WHEN undefined_table OR undefined_column THEN NULL; END;

            WHEN v_source_type = 'bank_vipiska' THEN
                BEGIN
                    SELECT matched_contact_id INTO v_tmp_uuid
                    FROM bank_statement_transactions WHERE id = v_source_id;
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
