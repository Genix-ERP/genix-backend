-- 337_profit_tax_calc.sql
-- Persisted snapshots of profit-tax calculations.
--
-- The GET /profit-tax endpoint re-computes on every read from the live
-- expenses + revenue tables, but accountants also need to "close" a period
-- and keep an audit trail of the numbers that were used for reporting.
-- Rows in this table are those closed snapshots — one row per
-- (tenant, period_key, period_type). `period_key` is an ISO-8601 prefix:
-- 'YYYY-MM' for monthly, 'YYYY-Qn' for quarterly, 'YYYY' for yearly.
--
-- Formula (see §6.4 of ТЗ_Ish_Haqi_Soliq_Tolik.docx):
--   accounting_profit = income − (recognized_exp + unrecognized_exp)
--   tax_base          = income − recognized_exp      ← key difference
--   tax_amount        = tax_base × (rate_snapshot/100)
--   net_profit        = accounting_profit − tax_amount

CREATE TABLE IF NOT EXISTS profit_tax_calc (
    id                     UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id              UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id        UUID REFERENCES organizations(id) ON DELETE SET NULL,

    period_type            VARCHAR(10) NOT NULL
                             CHECK (period_type IN ('month','quarter','year')),
    period_key             VARCHAR(10) NOT NULL,  -- e.g. '2026-04', '2026-Q2', '2026'
    period_start           DATE NOT NULL,
    period_end             DATE NOT NULL,

    income                 NUMERIC(18,2) NOT NULL DEFAULT 0,
    recognized_exp         NUMERIC(18,2) NOT NULL DEFAULT 0,
    unrecognized_exp       NUMERIC(18,2) NOT NULL DEFAULT 0,

    accounting_profit      NUMERIC(18,2) NOT NULL DEFAULT 0,
    tax_base               NUMERIC(18,2) NOT NULL DEFAULT 0,
    tax_amount             NUMERIC(18,2) NOT NULL DEFAULT 0,
    net_profit             NUMERIC(18,2) NOT NULL DEFAULT 0,

    -- Rate in effect when the snapshot was taken (percent, e.g. 15.00).
    rate_snapshot          NUMERIC(9,4) NOT NULL DEFAULT 15.0000,

    notes                  TEXT,
    created_by             UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- A tenant can re-snapshot the same period (e.g. after correcting an
-- expense); keep the most recent row authoritative by enforcing uniqueness
-- on the logical period rather than re-computing the old snapshot's id.
CREATE UNIQUE INDEX IF NOT EXISTS uq_profit_tax_calc_tenant_period
    ON profit_tax_calc (tenant_id, period_type, period_key);

CREATE INDEX IF NOT EXISTS idx_profit_tax_calc_tenant_period_start
    ON profit_tax_calc (tenant_id, period_start DESC);
