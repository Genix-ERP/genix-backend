-- +migrate Up
-- Bank vipiska (Excel statement) import — Phase 1: parse + auto-classify + review.
-- Reference: TZ "Bank ko'chirmasini (vipiska) ERP tizimiga yuklash" (ELPARVAR).
--
-- Extends the existing bank_statement_transactions with the extra columns the
-- vipiska review screen needs (operation/MFO codes + the auto-detected ERP
-- debit/credit accounts), and adds a configurable classification rules table.

ALTER TABLE bank_statement_transactions
    ADD COLUMN IF NOT EXISTS op_code            VARCHAR(16),
    ADD COLUMN IF NOT EXISTS mfo                VARCHAR(16),
    ADD COLUMN IF NOT EXISTS purpose_code       VARCHAR(32),
    ADD COLUMN IF NOT EXISTS account_prefix     VARCHAR(8),
    ADD COLUMN IF NOT EXISTS debet_account_id   UUID,
    ADD COLUMN IF NOT EXISTS kredit_account_id  UUID,
    ADD COLUMN IF NOT EXISTS debet_account_code VARCHAR(16),
    ADD COLUMN IF NOT EXISTS kredit_account_code VARCHAR(16),
    ADD COLUMN IF NOT EXISTS category           VARCHAR(128);

-- Configurable classification rules. A NULL tenant_id row is a GLOBAL default
-- that applies to every tenant; a tenant-specific row (tenant_id set) overrides
-- the global defaults (checked first, lower priority value = checked earlier).
CREATE TABLE IF NOT EXISTS bank_classification_rules (
    id                 UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id          UUID REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id    UUID REFERENCES organizations(id) ON DELETE CASCADE,
    priority           INT NOT NULL DEFAULT 100,
    match_type         VARCHAR(20) NOT NULL,            -- account_prefix | inn | keyword
    match_value        VARCHAR(255) NOT NULL,
    direction          VARCHAR(8) NOT NULL DEFAULT 'any', -- in | out | any
    target_account_code VARCHAR(16) NOT NULL,           -- the NON-bank ERP account
    category           VARCHAR(128),
    counterparty_type  VARCHAR(32),
    is_active          BOOLEAN NOT NULL DEFAULT true,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_bank_class_rules_lookup
    ON bank_classification_rules (tenant_id, is_active, priority);

-- Global default rules derived from the ELPARVAR vipiska sample. The bank side
-- of every entry is the bank account (5110); these target accounts are the
-- OTHER leg. Direction is from the company's point of view (in = money received).
INSERT INTO bank_classification_rules
    (tenant_id, priority, match_type, match_value, direction, target_account_code, category)
VALUES
    (NULL, 10, 'account_prefix', '29824', 'in',  '6310', 'Uy-joy to''lovi (mijoz avansi)'),
    (NULL, 10, 'account_prefix', '17409', 'in',  '6310', 'Uy-joy to''lovi (mijoz avansi)'),
    (NULL, 20, 'account_prefix', '20208', 'out', '6010', 'Yetkazib beruvchiga to''lov'),
    (NULL, 20, 'account_prefix', '23120', 'out', '6990', 'Jismoniy shaxsga qaytarish'),
    (NULL, 20, 'account_prefix', '16401', 'out', '9430', 'Bank komissiyasi'),
    -- keyword fallbacks (checked after prefixes)
    (NULL, 50, 'keyword', 'комисси', 'out', '9430', 'Bank komissiyasi'),
    (NULL, 50, 'keyword', 'komissi', 'out', '9430', 'Bank komissiyasi'),
    (NULL, 60, 'keyword', 'уй жой',  'in',  '6310', 'Uy-joy to''lovi (mijoz avansi)'),
    (NULL, 60, 'keyword', 'uy joy',  'in',  '6310', 'Uy-joy to''lovi (mijoz avansi)'),
    (NULL, 60, 'keyword', 'uy-joy',  'in',  '6310', 'Uy-joy to''lovi (mijoz avansi)')
ON CONFLICT DO NOTHING;
