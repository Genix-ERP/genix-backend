-- 443_contracts_v2.sql
--
-- Shartnomalar (Contracts) module rebuild — data model phase.
-- Full background: docs/shartnomalar-audit.md (repo root /docs).
--
-- What this migration does:
--   1. procurement_contracts becomes THE contracts table:
--      + direction ('income' kirim / 'expense' chiqim)
--      + responsible_employee_id → employees
--      + signed_date, archived_at
--   2. Lifecycle status model with a CHECK backstop:
--      draft → negotiation → signing → active → completed / cancelled
--      (+ expired, set by the scheduler). Existing rows remapped:
--      terminated→cancelled, renewed/approved→active.
--   3. contract_amendments reshaped (the 017 table was orphaned — zero
--      code paths ever wrote it; if a deployment somehow has rows the
--      old table is renamed, never dropped with data).
--   4. contract_files — versioned file attachments (never overwrite),
--      with a per-version AI summary cache.
--   5. contract_links — polymorphic links to CRM deals, construction
--      objects, purchase/sale orders.
--   6. contract_expiry_notifications — per-threshold notification
--      dedupe (30/14/3 days).
--   7. contract_id added to sales_invoices, purchase_invoices,
--      sales_orders, purchase_orders. NOTE: the GL enrichment trigger
--      (393) already probes sales_invoices.contract_id /
--      purchase_invoices.contract_id / purchase_orders.contract_id
--      inside EXCEPTION guards — these columns existing turns those
--      steps from silent no-ops into working enrichment.
--   8. Legacy `contracts` rows (012) merged into procurement_contracts
--      (all rows, including soft-deleted, so FK repointing is safe).
--      The legacy table itself is kept frozen; a later cleanup
--      migration may drop it once nothing references it in any
--      deployment.
--   9. journal_entry_lines.fk_jel_contract repointed from legacy
--      `contracts` to procurement_contracts.
--  10. fn_auto_enrich_journal_line_analytics() recreated: the vendor
--      fallback now walks procurement_contracts AND regains the
--      tenant_id predicate that migration 390 had and 393 lost.
--
-- Companion code changes (same commit): handler/contracts.go rewrite,
-- purchase_orders.go spot-contract writer now targets
-- procurement_contracts, workflow_rules.go checkExpiringContracts fixed
-- (it referenced the nonexistent contracts.contact_id column and has
-- errored on every scan since it shipped).

-- ── 1. New columns on procurement_contracts ─────────────────────────────
ALTER TABLE procurement_contracts
    ADD COLUMN IF NOT EXISTS direction VARCHAR(10) NOT NULL DEFAULT 'expense',
    ADD COLUMN IF NOT EXISTS responsible_employee_id UUID REFERENCES employees(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS signed_date DATE,
    ADD COLUMN IF NOT EXISTS archived_at TIMESTAMP WITH TIME ZONE;

ALTER TABLE procurement_contracts DROP CONSTRAINT IF EXISTS chk_procurement_contracts_direction;
ALTER TABLE procurement_contracts
    ADD CONSTRAINT chk_procurement_contracts_direction
    CHECK (direction IN ('income', 'expense'));

-- ── 2. Status remap + CHECK backstop ────────────────────────────────────
UPDATE procurement_contracts SET status = CASE
    WHEN status IN ('draft','negotiation','signing','active','completed','cancelled','expired') THEN status
    WHEN status = 'terminated' THEN 'cancelled'
    WHEN status IN ('renewed','approved') THEN 'active'
    ELSE 'draft'
END;

ALTER TABLE procurement_contracts DROP CONSTRAINT IF EXISTS chk_procurement_contracts_status;
ALTER TABLE procurement_contracts
    ADD CONSTRAINT chk_procurement_contracts_status
    CHECK (status IN ('draft','negotiation','signing','active','completed','cancelled','expired'));

-- ── 3. contract_amendments reshape ──────────────────────────────────────
-- The 017 table is orphaned (no handler ever referenced it). Preserve it
-- if any deployment somehow has rows; otherwise drop and recreate.
DO $$
DECLARE has_rows BOOLEAN;
BEGIN
    SELECT EXISTS(SELECT 1 FROM contract_amendments) INTO has_rows;
    IF has_rows THEN
        ALTER TABLE contract_amendments RENAME TO contract_amendments_017_legacy;
        ALTER INDEX IF EXISTS idx_contract_amendments_tenant RENAME TO idx_ca_017_legacy_tenant;
        ALTER INDEX IF EXISTS idx_contract_amendments_contract RENAME TO idx_ca_017_legacy_contract;
        ALTER INDEX IF EXISTS idx_contract_amendments_status RENAME TO idx_ca_017_legacy_status;
    ELSE
        DROP TABLE contract_amendments;
    END IF;
END $$;

CREATE TABLE contract_amendments (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    contract_id UUID NOT NULL REFERENCES procurement_contracts(id) ON DELETE CASCADE,
    number VARCHAR(100) NOT NULL,          -- official annex number ("Ilova 1", "QK-2")
    date DATE NOT NULL,
    amount_delta DECIMAL(20, 4),           -- NULL = no amount change
    description TEXT,
    file_id TEXT REFERENCES uploaded_files(id) ON DELETE SET NULL,
    file_name VARCHAR(500),
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX idx_contract_amendments_tenant ON contract_amendments(tenant_id);
CREATE INDEX idx_contract_amendments_contract ON contract_amendments(contract_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX uq_contract_amendments_number ON contract_amendments(contract_id, number) WHERE deleted_at IS NULL;

-- ── 4. contract_files — versioned, never overwritten ────────────────────
CREATE TABLE IF NOT EXISTS contract_files (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    contract_id UUID NOT NULL REFERENCES procurement_contracts(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    file_id TEXT NOT NULL REFERENCES uploaded_files(id) ON DELETE CASCADE,
    original_name VARCHAR(500) NOT NULL,
    file_size BIGINT NOT NULL DEFAULT 0,
    mime_type VARCHAR(255),
    ai_summary TEXT,                        -- cached per file version
    ai_summary_at TIMESTAMP WITH TIME ZONE,
    uploaded_by UUID REFERENCES users(id),
    uploaded_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(contract_id, version)
);

CREATE INDEX IF NOT EXISTS idx_contract_files_tenant ON contract_files(tenant_id);
CREATE INDEX IF NOT EXISTS idx_contract_files_contract ON contract_files(contract_id) WHERE deleted_at IS NULL;

-- ── 5. contract_links — polymorphic cross-module links ──────────────────
-- linked_id is VARCHAR to span UUID-keyed modules and the BIGINT-keyed
-- construction tables (same convention as task_links, migration 439).
CREATE TABLE IF NOT EXISTS contract_links (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    contract_id UUID NOT NULL REFERENCES procurement_contracts(id) ON DELETE CASCADE,
    linked_module VARCHAR(50) NOT NULL CHECK (linked_module IN ('crm_deal','construction_object','purchase_order','sale_order')),
    linked_id VARCHAR(100) NOT NULL,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(contract_id, linked_module, linked_id)
);

CREATE INDEX IF NOT EXISTS idx_contract_links_tenant ON contract_links(tenant_id);
CREATE INDEX IF NOT EXISTS idx_contract_links_target ON contract_links(tenant_id, linked_module, linked_id);

-- ── 6. Expiry-notification dedupe (30/14/3 day thresholds) ──────────────
CREATE TABLE IF NOT EXISTS contract_expiry_notifications (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    contract_id UUID NOT NULL REFERENCES procurement_contracts(id) ON DELETE CASCADE,
    threshold_days INTEGER NOT NULL,
    sent_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(contract_id, threshold_days)
);

-- ── 7. contract_id on financial documents ───────────────────────────────
ALTER TABLE sales_invoices    ADD COLUMN IF NOT EXISTS contract_id UUID REFERENCES procurement_contracts(id) ON DELETE SET NULL;
ALTER TABLE purchase_invoices ADD COLUMN IF NOT EXISTS contract_id UUID REFERENCES procurement_contracts(id) ON DELETE SET NULL;
ALTER TABLE sales_orders      ADD COLUMN IF NOT EXISTS contract_id UUID REFERENCES procurement_contracts(id) ON DELETE SET NULL;
ALTER TABLE purchase_orders   ADD COLUMN IF NOT EXISTS contract_id UUID REFERENCES procurement_contracts(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_sales_invoices_contract    ON sales_invoices(contract_id)    WHERE contract_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_purchase_invoices_contract ON purchase_invoices(contract_id) WHERE contract_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_sales_orders_contract      ON sales_orders(contract_id)      WHERE contract_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_purchase_orders_contract   ON purchase_orders(contract_id)   WHERE contract_id IS NOT NULL;

-- ── 8. Merge legacy `contracts` (012) into procurement_contracts ────────
-- Defensively rename any legacy contract number that would collide with
-- an existing procurement contract under UNIQUE(tenant_id, organization_id,
-- contract_number). NULLs compare distinct in the unique index, so only
-- non-NULL-org collisions matter, but we guard both.
UPDATE contracts c SET contract_number = c.contract_number || '-L'
WHERE EXISTS (
    SELECT 1 FROM procurement_contracts p
    WHERE p.tenant_id = c.tenant_id
      AND p.organization_id IS NOT DISTINCT FROM c.organization_id
      AND p.contract_number = c.contract_number
      AND p.id <> c.id
);

INSERT INTO procurement_contracts (
    id, tenant_id, organization_id, contract_number, vendor_id, vendor_name,
    title, description, contract_type, start_date, end_date, value, currency,
    payment_terms, terms, auto_renewal, renewal_term_days, status, attachments,
    created_by, created_at, updated_at, deleted_at, direction
)
SELECT
    c.id, c.tenant_id, c.organization_id, c.contract_number, c.supplier_id,
    COALESCE(c.supplier_name, ''), c.title, c.description,
    COALESCE(c.contract_type, 'fixed'), c.start_date, c.end_date, c.value,
    COALESCE(c.currency, 'UZS'), c.payment_terms, c.terms,
    COALESCE(c.auto_renew, false), c.renewal_notice_days,
    CASE
        WHEN c.status IN ('draft','negotiation','signing','active','completed','cancelled','expired') THEN c.status
        WHEN c.status = 'terminated' THEN 'cancelled'
        WHEN c.status IN ('renewed','approved') THEN 'active'
        ELSE 'draft'
    END,
    COALESCE(c.attachments, '[]'::jsonb),
    c.created_by, c.created_at, c.updated_at, c.deleted_at, 'expense'
FROM contracts c
WHERE NOT EXISTS (SELECT 1 FROM procurement_contracts p WHERE p.id = c.id);

-- ── 9. Repoint journal_entry_lines FK to procurement_contracts ──────────
-- Safe: step 8 copied every legacy row (including soft-deleted), and the
-- old FK was ON DELETE SET NULL, so every referenced id now exists in
-- procurement_contracts.
ALTER TABLE journal_entry_lines DROP CONSTRAINT IF EXISTS fk_jel_contract;
ALTER TABLE journal_entry_lines
    ADD CONSTRAINT fk_jel_contract FOREIGN KEY (contract_id)
    REFERENCES procurement_contracts(id) ON DELETE SET NULL;

-- ── 9b. Rename the legacy trigger event on stored automation rules ──────
-- 'contract.expiring' never fired (the scanner read the orphaned legacy
-- table), so this rename cannot change observed behavior — it just points
-- old rules at the event that actually fires now.
UPDATE workflow_rules SET trigger_event = 'contracts.expiring_soon'
WHERE trigger_event = 'contract.expiring';

-- ── 10. Useful indexes for the registry & scheduler ─────────────────────
CREATE INDEX IF NOT EXISTS idx_procurement_contracts_responsible
    ON procurement_contracts(responsible_employee_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_procurement_contracts_expiry
    ON procurement_contracts(tenant_id, end_date)
    WHERE deleted_at IS NULL AND archived_at IS NULL AND status = 'active';

-- ── 11. GL enrichment trigger: contract fallback fixed ──────────────────
-- Recreates fn_auto_enrich_journal_line_analytics() verbatim from 393
-- except the shartnoma step 3 fallback: it now walks
-- procurement_contracts (the new FK target) and regains the tenant_id
-- predicate that 390 introduced and 393 dropped.
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

COMMENT ON FUNCTION fn_auto_enrich_journal_line_analytics() IS
    'TT §4.5 auto-fill for journal_entry_lines analytics. Migration 443: '
    'shartnoma fallback walks procurement_contracts with tenant predicate '
    '(fixes the 393 regression); direct contract_id columns on invoices '
    'now exist so steps 1-2 actually fire.';
