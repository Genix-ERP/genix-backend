-- Kassa schema repair — GET /cash/orders returns 500 on the genix deployment.
--
-- THE FAILURE
-- ListCashOrders / ListCashRegisters / GetCashBook (cash_kassa.go, bbdb477) all
-- 500 on genix while working on yuksalish. Every column and table they touch is
-- present in 003_finance_modules.sql, so the statements are well-formed against
-- the schema as the repo defines it — which means the deployed database does not
-- match that definition.
--
-- WHY 003 CANNOT FIX ITSELF
-- The three kassa tables live in 003 as CREATE TABLE IF NOT EXISTS. Two ways
-- that silently leaves a database wrong, and 003 can repair neither:
--
--   1. The table is absent. 003 is long since recorded as applied, and the
--      runner skips any version already in schema_migrations, so re-running is
--      not an option. Editing 003 in place does nothing for the same reason —
--      that is exactly how this class of bug is created.
--
--   2. The table exists in an older shape. IF NOT EXISTS makes CREATE a no-op
--      against a pre-existing table, so a cash_orders lacking deleted_at or
--      organization_id keeps its old columns and 003 still reports success.
--
-- Both produce a runtime 500 with a migration history that looks clean. Hence a
-- NEW version number: this file must run on databases that already have 003.
--
-- This migration is a no-op on a correct database and repairs either failure on
-- a wrong one, so it is safe to apply everywhere without first diagnosing which
-- deployment is in which state.
--
-- Added columns are deliberately NULLABLE and carry no CHECK constraints, even
-- where 003 declares NOT NULL / CHECK. ADD COLUMN ... NOT NULL fails outright on
-- a table that already has rows, and retrofitting a CHECK fails on any row that
-- violates it — either would abort the repair and leave the endpoints broken.
-- The goal here is to make the reads work; tightening constraints on existing
-- data is a separate, data-dependent job.

-- ── 1. Tables, in FK order ──────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS cash_registers (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    name VARCHAR(255) NOT NULL,
    code VARCHAR(50),
    currency VARCHAR(10) NOT NULL DEFAULT 'UZS',
    limit_amount DECIMAL(20, 4) DEFAULT 0,
    current_balance DECIMAL(20, 4) DEFAULT 0,
    is_active BOOLEAN DEFAULT true,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, code)
);

CREATE TABLE IF NOT EXISTS cash_orders (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    cash_register_id UUID NOT NULL REFERENCES cash_registers(id) ON DELETE RESTRICT,
    order_number VARCHAR(50) NOT NULL,
    order_type VARCHAR(10) NOT NULL CHECK (order_type IN ('pko', 'rko')),
    order_date DATE NOT NULL DEFAULT CURRENT_DATE,
    amount DECIMAL(20, 4) NOT NULL CHECK (amount > 0),
    currency VARCHAR(10) NOT NULL DEFAULT 'UZS',
    partner_id UUID REFERENCES contacts(id) ON DELETE SET NULL,
    account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
    description TEXT NOT NULL,
    cashier_id UUID REFERENCES users(id),
    journal_entry_id UUID REFERENCES journal_entries(id) ON DELETE SET NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'confirmed', 'cancelled')),
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, order_number)
);

CREATE TABLE IF NOT EXISTS cash_book_entries (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    cash_register_id UUID NOT NULL REFERENCES cash_registers(id) ON DELETE CASCADE,
    entry_date DATE NOT NULL,
    opening_balance DECIMAL(20, 4) NOT NULL DEFAULT 0,
    total_income DECIMAL(20, 4) NOT NULL DEFAULT 0,
    total_expense DECIMAL(20, 4) NOT NULL DEFAULT 0,
    closing_balance DECIMAL(20, 4) GENERATED ALWAYS AS (opening_balance + total_income - total_expense) STORED,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, cash_register_id, entry_date)
);

-- ── 2. Column drift on a pre-existing table ─────────────────────────────
-- Exactly the columns cash_kassa.go selects and filters on. Anything already
-- present is skipped.
ALTER TABLE cash_registers
    ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS code            VARCHAR(50),
    ADD COLUMN IF NOT EXISTS currency        VARCHAR(10) DEFAULT 'UZS',
    ADD COLUMN IF NOT EXISTS limit_amount    DECIMAL(20, 4) DEFAULT 0,
    ADD COLUMN IF NOT EXISTS current_balance DECIMAL(20, 4) DEFAULT 0,
    ADD COLUMN IF NOT EXISTS is_active       BOOLEAN DEFAULT true,
    ADD COLUMN IF NOT EXISTS created_at      TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    ADD COLUMN IF NOT EXISTS updated_at      TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    ADD COLUMN IF NOT EXISTS deleted_at      TIMESTAMP WITH TIME ZONE;

ALTER TABLE cash_orders
    ADD COLUMN IF NOT EXISTS organization_id  UUID REFERENCES organizations(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS cash_register_id UUID REFERENCES cash_registers(id) ON DELETE RESTRICT,
    ADD COLUMN IF NOT EXISTS order_number     VARCHAR(50),
    ADD COLUMN IF NOT EXISTS order_type       VARCHAR(10),
    ADD COLUMN IF NOT EXISTS order_date       DATE DEFAULT CURRENT_DATE,
    ADD COLUMN IF NOT EXISTS amount           DECIMAL(20, 4) DEFAULT 0,
    ADD COLUMN IF NOT EXISTS currency         VARCHAR(10) DEFAULT 'UZS',
    ADD COLUMN IF NOT EXISTS partner_id       UUID REFERENCES contacts(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS account_id       UUID REFERENCES accounts(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS description      TEXT,
    ADD COLUMN IF NOT EXISTS cashier_id       UUID REFERENCES users(id),
    ADD COLUMN IF NOT EXISTS journal_entry_id UUID REFERENCES journal_entries(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS status           VARCHAR(20) DEFAULT 'draft',
    ADD COLUMN IF NOT EXISTS created_at       TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    ADD COLUMN IF NOT EXISTS updated_at       TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    ADD COLUMN IF NOT EXISTS deleted_at       TIMESTAMP WITH TIME ZONE;

-- cash_book_entries has NO deleted_at and NO organization_id by design, and the
-- handler does not reference either — do not "helpfully" add them here, or the
-- next reader will assume the soft-delete filter was forgotten.
ALTER TABLE cash_book_entries
    ADD COLUMN IF NOT EXISTS opening_balance DECIMAL(20, 4) DEFAULT 0,
    ADD COLUMN IF NOT EXISTS total_income    DECIMAL(20, 4) DEFAULT 0,
    ADD COLUMN IF NOT EXISTS total_expense   DECIMAL(20, 4) DEFAULT 0,
    ADD COLUMN IF NOT EXISTS created_at      TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    ADD COLUMN IF NOT EXISTS updated_at      TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP;

-- closing_balance is GENERATED ALWAYS ... STORED. Added separately because a
-- generated column cannot be declared in a multi-clause ALTER alongside the
-- columns its expression depends on — those must already exist. Guarded by a DO
-- block rather than ADD COLUMN IF NOT EXISTS so that an existing PLAIN
-- closing_balance column is left alone instead of erroring: the handler only
-- ever reads this column, never writes it, so a plain column holding the same
-- arithmetic still serves.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'cash_book_entries' AND column_name = 'closing_balance'
    ) THEN
        ALTER TABLE cash_book_entries
            ADD COLUMN closing_balance DECIMAL(20, 4)
            GENERATED ALWAYS AS (opening_balance + total_income - total_expense) STORED;
    END IF;
END $$;

-- ── 3. Indexes ──────────────────────────────────────────────────────────
-- 003 creates these without IF NOT EXISTS, so they are absent exactly when the
-- table was absent.
CREATE INDEX IF NOT EXISTS idx_cash_registers_tenant ON cash_registers(tenant_id);
CREATE INDEX IF NOT EXISTS idx_cash_orders_tenant    ON cash_orders(tenant_id);
CREATE INDEX IF NOT EXISTS idx_cash_orders_register  ON cash_orders(cash_register_id);
CREATE INDEX IF NOT EXISTS idx_cash_orders_date      ON cash_orders(tenant_id, order_date);
CREATE INDEX IF NOT EXISTS idx_cash_orders_type      ON cash_orders(tenant_id, order_type);
CREATE INDEX IF NOT EXISTS idx_cash_orders_partner   ON cash_orders(partner_id);
CREATE INDEX IF NOT EXISTS idx_cash_book_lookup      ON cash_book_entries(tenant_id, cash_register_id, entry_date);
