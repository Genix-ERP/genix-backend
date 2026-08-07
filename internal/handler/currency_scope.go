package handler

import (
	"github.com/google/uuid"
)

// Tenant-aware currency resolution.
//
// `currencies` is a global catalogue: code, name, symbol and decimal_places are
// facts about the currency itself and are the same for every tenant, and 23 FK
// columns across the schema depend on its ids staying stable (see migration
// 471). What IS per-tenant is policy — whether a tenant transacts in a currency
// (is_active) and what it reports in (is_base_currency) — and that lives in
// tenant_currencies.
//
// Every read of those two fields must go through this file. Before migration
// 471 there were nine separate copies of
//
//	SELECT id FROM currencies WHERE is_base_currency = true LIMIT 1
//
// scattered across finance.go, finance_extra.go, purchase_invoices.go,
// sales_invoices.go and purchase_orders.go, each of them tenant-blind: they
// returned whatever the last tenant to press "set as base" had chosen. Keeping
// the lookup in one place is what makes it possible to state that no
// tenant-blind copy survives.

// baseCurrencySubquery is the scalar subquery for "this tenant's base currency
// id". It takes exactly one bind parameter (the tenant id) at the position the
// caller formats in, and is written to be inlined into a larger statement.
//
// The COALESCE chain is deliberate and ordered by decreasing authority:
//  1. the tenant's own explicit choice in tenant_currencies;
//  2. the legacy global flag, for a tenant provisioned after the backfill or a
//     currency added to the catalogue later;
//  3. UZS, which migration 124 guarantees exists and which the handlers already
//     hardcoded as their fallback.
//
// Without step 3 a tenant with no configured base would make every rate lateral
// join on NULL and silently report every foreign-currency document as rate-less.
const baseCurrencySubquery = `(
    SELECT COALESCE(
        (SELECT tc.currency_id FROM tenant_currencies tc
          WHERE tc.tenant_id = %[1]s AND tc.is_base_currency LIMIT 1),
        (SELECT c.id FROM currencies c WHERE c.is_base_currency = true LIMIT 1),
        (SELECT c.id FROM currencies c WHERE c.code = 'UZS' LIMIT 1)
    )
)`

// baseCurrencyID resolves the tenant's base currency using the same precedence
// as baseCurrencySubquery. Returns uuid.Nil when the catalogue is empty, which
// callers must treat as "cannot convert" rather than as a zero rate.
func (h *Handler) baseCurrencyID(tenantID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := h.db.QueryRow(`
		SELECT COALESCE(
		    (SELECT tc.currency_id FROM tenant_currencies tc
		      WHERE tc.tenant_id = $1 AND tc.is_base_currency LIMIT 1),
		    (SELECT c.id FROM currencies c WHERE c.is_base_currency = true LIMIT 1),
		    (SELECT c.id FROM currencies c WHERE c.code = 'UZS' LIMIT 1)
		)`, tenantID).Scan(&id)
	return id, err
}

// tenantCurrencyIDs returns the tenant's active currencies keyed by ISO code.
// A currency with no tenant_currencies row falls back to the catalogue's own
// is_active, so a currency added to the catalogue after a tenant was
// provisioned is visible to that tenant instead of silently missing.
func (h *Handler) tenantCurrencyIDs(tenantID uuid.UUID) (map[string]uuid.UUID, error) {
	rows, err := h.db.Query(`
		SELECT c.code, c.id
		FROM currencies c
		LEFT JOIN tenant_currencies tc
		       ON tc.currency_id = c.id AND tc.tenant_id = $1
		WHERE COALESCE(tc.is_active, c.is_active, true)`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]uuid.UUID)
	for rows.Next() {
		var code string
		var id uuid.UUID
		if err := rows.Scan(&code, &id); err != nil {
			continue
		}
		out[code] = id
	}
	return out, rows.Err()
}

// setTenantBaseCurrency makes currencyID the tenant's base. The unique partial
// index idx_tenant_currencies_one_base allows only one base row per tenant, so
// the unset and the set must happen in one transaction — the old code did them
// as two bare UPDATEs against the global table, and a crash in between left a
// tenant with zero or two base currencies.
func (h *Handler) setTenantBaseCurrency(tenantID, currencyID uuid.UUID) error {
	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		UPDATE tenant_currencies SET is_base_currency = false, updated_at = NOW()
		WHERE tenant_id = $1 AND is_base_currency AND currency_id <> $2`,
		tenantID, currencyID); err != nil {
		return err
	}

	// A base currency the tenant does not transact in is not a coherent state,
	// so setting base implies active.
	if _, err := tx.Exec(`
		INSERT INTO tenant_currencies (tenant_id, currency_id, is_active, is_base_currency)
		VALUES ($1, $2, true, true)
		ON CONFLICT (tenant_id, currency_id) DO UPDATE
		   SET is_base_currency = true, is_active = true, updated_at = NOW()`,
		tenantID, currencyID); err != nil {
		return err
	}

	return tx.Commit()
}

// setTenantCurrencyActive enables or disables a currency for one tenant only.
func (h *Handler) setTenantCurrencyActive(tenantID, currencyID uuid.UUID, active bool) error {
	_, err := h.db.Exec(`
		INSERT INTO tenant_currencies (tenant_id, currency_id, is_active, is_base_currency)
		VALUES ($1, $2, $3, false)
		ON CONFLICT (tenant_id, currency_id) DO UPDATE
		   SET is_active = EXCLUDED.is_active, updated_at = NOW()`,
		tenantID, currencyID, active)
	return err
}
