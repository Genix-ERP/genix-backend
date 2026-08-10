-- Zaxiralarni baholash usullari, bosqich 1: qatlamlar va iste'mol modeli.
--
-- WHAT THIS IS
-- The valuation layer is the core of the whole design (plan §1.2): one
-- immutable record per receipt, carrying how much of it is still unissued.
-- Layers are kept under EVERY method, not only FIFO — that is what makes the
-- audit trail, the Σ invariant and a future method change all cheap. The
-- methods differ only in how a movement's cost is computed.
--
-- WHY MONEY IS NUMERIC(20,2) WHILE QUANTITY IS NUMERIC(20,4)
-- Plan §3.5: only quantities and SUMS are primary; a unit price is always
-- derived and never stored as truth. Three units bought for 10 000 is
-- 3 333,33(3) each — storing that per-unit price guarantees a permanent
-- disagreement with the ledger, which posts in tiyin. Storing the sums at
-- currency precision and deriving the unit price on read makes
-- 3 333,33 + 3 333,34 + 3 333,33 = 10 000,00 exactly.
--
-- SCOPE
-- Layers are per (tenant, organization, product) — company-wide, not per
-- warehouse (plan §3.2). A transfer between two warehouses of the same company
-- moves no value and writes no layer. Note this is the ONE place the plan's
-- word "kompaniya" had to be interpreted: this schema is multi-organization
-- inside a tenant, so organization_id is the company.
--
-- This migration adds no journal entries and rewrites no existing valuation.
-- The current inventory.unit_cost average and inventory_lots keep working
-- untouched; nothing reads these tables until phase 2 wires them in.

-- ---------------------------------------------------------------- layers ---
CREATE TABLE IF NOT EXISTS stock_valuation_layers (
    id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id   UUID REFERENCES organizations(id) ON DELETE CASCADE,
    product_id        UUID NOT NULL REFERENCES products(id) ON DELETE RESTRICT,

    layer_date        DATE NOT NULL,
    -- What created this layer: receipt, customer_return, stock_count_surplus,
    -- opening_balance, revaluation.
    source_type       VARCHAR(40) NOT NULL,
    source_doc_id     UUID,
    source_doc_number VARCHAR(100),

    -- Original quantity and value. unit_cost is stored for reference only —
    -- every calculation reads `value`, never quantity × unit_cost.
    quantity          NUMERIC(20, 4) NOT NULL,
    unit_cost         NUMERIC(20, 4) NOT NULL DEFAULT 0,
    value             NUMERIC(20, 2) NOT NULL,

    -- The part not yet issued. FIFO drains these oldest-first; the other
    -- methods drain them too, for audit.
    remaining_qty     NUMERIC(20, 4) NOT NULL,
    remaining_value   NUMERIC(20, 2) NOT NULL,

    -- Phase 2 populates this. Kept here so a layer and its posting are always
    -- navigable in both directions.
    journal_entry_id  UUID REFERENCES journal_entries(id) ON DELETE SET NULL,

    -- A reversed layer is excluded from the invariant and from consumption.
    -- Documents are never deleted, only storno'd (plan §2.6).
    is_reversed       BOOLEAN NOT NULL DEFAULT false,
    reversed_by_id    UUID REFERENCES stock_valuation_layers(id) ON DELETE SET NULL,

    created_at        TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by        UUID REFERENCES users(id),

    -- Negative stock is forbidden (plan §2.5), so a layer can never be drained
    -- past empty. NOT VALID is deliberately NOT used: the table is new, so
    -- there is no legacy row to trip over and the constraint can be validated
    -- from the start.
    CONSTRAINT stock_valuation_layers_remaining_qty_check
        CHECK (remaining_qty >= 0 AND remaining_qty <= quantity),
    CONSTRAINT stock_valuation_layers_quantity_check
        CHECK (quantity > 0)
);

-- The FIFO drain order, as an index: date then id, exactly the order
-- open_layers() iterates (plan §3.1). Partial on the open layers because a
-- depleted one is never a candidate, which keeps the index small on a table
-- that only ever grows.
CREATE INDEX IF NOT EXISTS idx_svl_open_fifo
    ON stock_valuation_layers (tenant_id, organization_id, product_id, layer_date, id)
    WHERE remaining_qty > 0 AND is_reversed = false;

CREATE INDEX IF NOT EXISTS idx_svl_product_date
    ON stock_valuation_layers (tenant_id, product_id, layer_date);

CREATE INDEX IF NOT EXISTS idx_svl_source
    ON stock_valuation_layers (tenant_id, source_type, source_doc_id);

-- ----------------------------------------------------------- consumption ---
-- Which issue took how much from which layer. Required for the FIFO audit and
-- for returning goods AT THE ORIGINAL ISSUE COST rather than at today's price
-- (plan §3.1, §3.2).
CREATE TABLE IF NOT EXISTS stock_valuation_consumptions (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    product_id      UUID NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
    layer_id        UUID NOT NULL REFERENCES stock_valuation_layers(id) ON DELETE RESTRICT,

    issue_date      DATE NOT NULL,
    -- sale, supplier_return, scrap, stock_count_shortage, production_issue.
    source_type     VARCHAR(40) NOT NULL,
    source_doc_id   UUID,
    source_doc_number VARCHAR(100),

    quantity        NUMERIC(20, 4) NOT NULL CHECK (quantity > 0),
    -- The share of the layer's remaining_value this issue took. Computed by
    -- proportion of VALUE, never as quantity × a rounded unit price.
    value           NUMERIC(20, 2) NOT NULL,

    journal_entry_id UUID REFERENCES journal_entries(id) ON DELETE SET NULL,
    is_reversed     BOOLEAN NOT NULL DEFAULT false,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by      UUID REFERENCES users(id)
);

CREATE INDEX IF NOT EXISTS idx_svc_layer ON stock_valuation_consumptions (layer_id);
CREATE INDEX IF NOT EXISTS idx_svc_source
    ON stock_valuation_consumptions (tenant_id, source_type, source_doc_id);
CREATE INDEX IF NOT EXISTS idx_svc_product_date
    ON stock_valuation_consumptions (tenant_id, product_id, issue_date);

-- --------------------------------------------------------- method choice ---
-- The method is configured by hierarchy: company accounting policy (default)
-- overridden by product category. It is NEVER chosen on the product card
-- (plan §0) — the product only displays the effective method, read-only.
--
-- NULL means "inherit from the company policy", which lives in
-- tenant_settings.settings->'inventory_valuation' and is already read by
-- inventory_settings.go. That existing key stores 'aveco' (a long-standing
-- misspelling) or 'fifo'; readers must accept both spellings.
ALTER TABLE product_categories
    ADD COLUMN IF NOT EXISTS cost_method VARCHAR(10);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'product_categories_cost_method_check'
          AND conrelid = 'product_categories'::regclass
    ) THEN
        ALTER TABLE product_categories
            ADD CONSTRAINT product_categories_cost_method_check
            CHECK (cost_method IS NULL OR cost_method IN ('fifo', 'avco', 'standard'));
    END IF;
END $$;

-- LIFO is absent on purpose: BHMS № 4 and IAS 2 prohibit it (plan §0). The
-- CHECK is what stops it coming back through an import or a bulk edit.

-- ------------------------------------------------------- standard costing ---
-- Only the standard method needs these. standard_cost is the price the product
-- is valued at; products.cost_price stays what it is today (a purchase-price
-- reference) and is deliberately not reused — conflating them would make a
-- price-list edit silently revalue stock.
ALTER TABLE products
    ADD COLUMN IF NOT EXISTS standard_cost NUMERIC(20, 4) NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS product_standard_cost_history (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    product_id      UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    effective_date  DATE NOT NULL,
    old_cost        NUMERIC(20, 4) NOT NULL,
    new_cost        NUMERIC(20, 4) NOT NULL,
    -- Quantity on hand at the moment of the change and the revaluation it
    -- produced: Δ = Q × (new − old). Zero when stock was empty, in which case
    -- no posting is made at all.
    quantity_on_hand NUMERIC(20, 4) NOT NULL DEFAULT 0,
    revaluation_delta NUMERIC(20, 2) NOT NULL DEFAULT 0,
    journal_entry_id UUID REFERENCES journal_entries(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by      UUID REFERENCES users(id)
);

CREATE INDEX IF NOT EXISTS idx_pscost_product
    ON product_standard_cost_history (tenant_id, product_id, effective_date DESC);

-- ------------------------------------------------------------ AVCO state ---
-- (Q, V) per product, company-wide. The average is ALWAYS derived as V / Q and
-- never stored (plan §3.2): storing it accumulates rounding until the ledger
-- and the warehouse disagree.
CREATE TABLE IF NOT EXISTS product_avco_state (
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    -- NOT NULL with the nil UUID as its default, not a nullable column.
    -- Postgres promotes a PRIMARY KEY column to NOT NULL anyway, so leaving it
    -- nullable would only have hidden that from anyone reading this file; and
    -- two NULLs do not conflict in a UNIQUE index either, so a tenant without
    -- organizations would have silently accumulated one state row per receipt.
    -- Callers COALESCE their organization_id to this value.
    organization_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000',
    product_id      UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    quantity        NUMERIC(20, 4) NOT NULL DEFAULT 0,
    value           NUMERIC(20, 2) NOT NULL DEFAULT 0,
    -- The last known unit value, kept only so a product that has gone to zero
    -- can still answer "what did this cost" before its next receipt.
    last_unit_cost  NUMERIC(20, 4) NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, organization_id, product_id),
    CONSTRAINT product_avco_state_quantity_check CHECK (quantity >= 0),
    -- Q = 0 must imply V = 0 (plan §3.5, third golden rule). This is the
    -- invariant that catches accumulated rounding: if a full issue ever left
    -- value behind, this constraint fails the transaction rather than letting
    -- the tiyin drift into the ledger.
    CONSTRAINT product_avco_state_zero_check CHECK (quantity > 0 OR value = 0)
);

COMMENT ON TABLE stock_valuation_layers IS
    'Immutable per-receipt valuation layers. Sum of remaining_value over open layers must equal the stock value and the 2910 balance (plan section 1.3).';
COMMENT ON TABLE stock_valuation_consumptions IS
    'Which issue drew how much value from which layer. Required for the FIFO audit and for returns at the original issue cost.';
