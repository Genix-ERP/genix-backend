-- Per-tenant currency policy.
--
-- THE BUG
-- `currencies` is a single global table with no tenant_id, and the /currencies
-- routes carry no permission middleware at all. So any authenticated user of
-- any tenant could:
--   * rename or re-symbol USD for every tenant on the server;
--   * set is_active = false on a currency and remove it from every other
--     tenant's dropdowns;
--   * set is_base_currency, which RE-BASES EVERY TENANT — the base currency is
--     what every exchange-rate lateral converts to, so one tenant switching to
--     RUB silently changed the reported values of every other tenant's
--     multi-currency documents.
--
-- WHY NOT JUST ADD tenant_id TO currencies
-- 23 FK columns across the schema point at currencies(id) — purchase_orders,
-- sales_orders, pricelists, payroll salary_currency_id, exchange_rates (twice),
-- and so on. Making the catalogue per-tenant means splitting each global row
-- into one row per tenant and then repointing every one of those 23 columns on
-- every historical row. A single missed repoint silently changes what currency
-- a historical invoice was denominated in, and nothing would surface it. That
-- risk buys nothing: the identity of a currency is not tenant-specific. USD is
-- USD, with the same ISO code, name, symbol and 2 decimal places, for everyone.
--
-- THE SPLIT
-- What is genuinely per-tenant is POLICY, not identity:
--   * is_active         — "does my company transact in this currency"
--   * is_base_currency  — "what does my company report in"
-- Those two move here. `currencies` keeps only the objective catalogue fields,
-- and its ids stay stable so all 23 FKs are untouched.
--
-- The old global columns are deliberately LEFT IN PLACE, not dropped. They are
-- the fallback for a tenant that has no row here yet (a tenant provisioned
-- after this migration, or a currency added to the catalogue later), and
-- keeping them means this migration is reversible by simply not reading the
-- new table.

CREATE TABLE IF NOT EXISTS tenant_currencies (
    tenant_id        UUID    NOT NULL REFERENCES tenants(id)    ON DELETE CASCADE,
    currency_id      UUID    NOT NULL REFERENCES currencies(id) ON DELETE CASCADE,
    is_active        BOOLEAN NOT NULL DEFAULT true,
    is_base_currency BOOLEAN NOT NULL DEFAULT false,
    created_at       TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at       TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, currency_id)
);

-- At most one base currency per tenant, enforced by the database rather than by
-- the handler remembering to unset the previous one. The old code did that unset
-- in a bare UPDATE with no transaction and no constraint, so a crash between the
-- two statements left a tenant with zero or two base currencies.
CREATE UNIQUE INDEX IF NOT EXISTS idx_tenant_currencies_one_base
    ON tenant_currencies (tenant_id)
    WHERE is_base_currency;

CREATE INDEX IF NOT EXISTS idx_tenant_currencies_active
    ON tenant_currencies (tenant_id, currency_id)
    WHERE is_active;

-- Backfill: every existing tenant gets a row per catalogue currency carrying
-- today's global values, so behaviour on the day of deploy is byte-identical to
-- behaviour the day before. Only the ability of one tenant to change another's
-- changes.
INSERT INTO tenant_currencies (tenant_id, currency_id, is_active, is_base_currency)
SELECT t.id, c.id, COALESCE(c.is_active, true), COALESCE(c.is_base_currency, false)
FROM tenants t
CROSS JOIN currencies c
ON CONFLICT (tenant_id, currency_id) DO NOTHING;

-- A tenant whose catalogue had no base currency flagged at all would otherwise
-- come out of this with none, and every rate lateral (which joins on
-- to_currency_id = <base>) would return NULL. Fall back to UZS, which is what
-- the handlers already hardcode as the fallback (purchase_orders.go,
-- finance_extra.go) and what migration 124 guarantees exists.
INSERT INTO tenant_currencies (tenant_id, currency_id, is_active, is_base_currency)
SELECT t.id, c.id, true, true
FROM tenants t
CROSS JOIN currencies c
WHERE c.code = 'UZS'
  AND NOT EXISTS (
      SELECT 1 FROM tenant_currencies tc
      WHERE tc.tenant_id = t.id AND tc.is_base_currency
  )
ON CONFLICT (tenant_id, currency_id) DO UPDATE
   SET is_base_currency = true, is_active = true;

-- Permission node for currency administration. There was none, which is why the
-- route group ended up with no middleware at all.
--
-- Granted to every role that can already configure the chart of accounts
-- (finance:account:update): if you are trusted to define GL accounts you are
-- trusted to say which currencies the company transacts in. Deriving the grant
-- from an existing node rather than naming roles means no finance manager loses
-- access the moment this ships, while everyone else is now correctly denied.
INSERT INTO permissions (module, resource, action, description) VALUES
    ('finance', 'currency', 'read',   'View currencies and their rates'),
    ('finance', 'currency', 'update', 'Enable, disable and set the base currency for the tenant')
ON CONFLICT (module, resource, action) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT rp.role_id, p.id
FROM role_permissions rp
JOIN permissions src ON src.id = rp.permission_id
                    AND src.module = 'finance' AND src.resource = 'account' AND src.action = 'update'
CROSS JOIN permissions p
WHERE p.module = 'finance' AND p.resource = 'currency' AND p.action IN ('read', 'update')
ON CONFLICT (role_id, permission_id) DO NOTHING;

-- No grant for :read. The read routes are deliberately left ungated (see the
-- /currencies group in handler.go): after this migration the response carries
-- only ISO catalogue fields plus the calling tenant's own rates and flags, so
-- there is nothing cross-tenant left to protect, and gating it would 403 the
-- sales and purchase users who need currency codes to render an order. The
-- node is still defined above so the permission UI can show it and so
-- loadPermissions' resourceMap entry for finance:currency has a row to match.
