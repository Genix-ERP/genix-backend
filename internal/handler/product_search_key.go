package handler

import (
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"genix-backend/internal/middleware"
	"genix-backend/internal/pkg/response"
)

// =====================================================
// Product search_key helpers
//
// `search_key` is a short, normalised identifier used to link the
// "same logical product" across organisations within a tenant.
// Example: a construction smeta row "ПЛИТЫ ПЕРЕКРЫТИЙ 1 ПК 59.10-6ШВ С8"
// and a manufacturing product "ПБ-5.9*100а" can be tied together by
// setting the same search_key (e.g. "PK59106SHVC8" / "PB59100A") so
// that a purchase order from the construction org resolves to the
// manufacturing org's product on the sales side.
// =====================================================

// GenerateSearchKey derives a default search_key from a product name.
// Keeps Latin + Cyrillic letters and digits, uppercases, removes the
// rest. The output is truncated to 32 chars so the same numbers
// ("59106") are preserved — those are the distinguishing tokens in
// construction nomenclature (GOST codes, dimensions, grades).
//
// The result is deterministic: same name → same key. Users can edit
// it afterwards via the product form.
func GenerateSearchKey(name string) string {
	if name == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToUpper(r))
		}
		if b.Len() >= 32 {
			break
		}
	}
	return b.String()
}

// GenerateProductSearchKey — POST /products/:id/generate-search-key
// Recomputes (and persists) the search_key for an existing product.
// Returns the new key so the UI can refresh the field.
func (h *Handler) GenerateProductSearchKey(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	productID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid product id")
		return
	}

	var name string
	if err := h.db.QueryRow(
		`SELECT name FROM products WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		productID, tenantID,
	).Scan(&name); err != nil {
		response.NotFound(c, "Product not found")
		return
	}

	key := GenerateSearchKey(name)
	if _, err := h.db.Exec(
		`UPDATE products SET search_key = $1, updated_at = now() WHERE id = $2 AND tenant_id = $3`,
		key, productID, tenantID,
	); err != nil {
		h.log.Error("failed to persist generated search_key", "error", err)
		response.InternalError(c, "Failed to generate search key")
		return
	}

	response.Success(c, gin.H{"search_key": key})
}

// FindProductsBySearchKey — GET /products/by-search-key?key=XXX[&organization_id=...][&exclude_organization_id=...]
//
// Returns products within the tenant that share a given search_key.
// Two scoping modes:
//
//   - organization_id=<uuid> — restrict results to products that the
//     given organisation can actually use (linked via
//     product_organization_settings). This is the mode the sales/
//     purchase order form should use: the seller can only sell their
//     own products, and product pickers must respect that membership.
//
//   - exclude_organization_id=<uuid> — the inverse: return products
//     belonging to any OTHER organisation in the tenant. This is used
//     by the print-document enrichment to find the counterparty's
//     name for the same material.
//
// With no filter, returns every tenant-wide match (useful for admin
// tools and cross-org diagnostics).
func (h *Handler) FindProductsBySearchKey(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	key := strings.TrimSpace(c.Query("key"))
	if key == "" {
		response.BadRequest(c, "key query param is required")
		return
	}

	var orgFilter *uuid.UUID
	if s := strings.TrimSpace(c.Query("organization_id")); s != "" {
		if u, err := uuid.Parse(s); err == nil {
			orgFilter = &u
		}
	}
	var orgExclude *uuid.UUID
	if s := strings.TrimSpace(c.Query("exclude_organization_id")); s != "" {
		if u, err := uuid.Parse(s); err == nil {
			orgExclude = &u
		}
	}

	query := `
		SELECT DISTINCT p.id, p.name, p.code,
		       COALESCE(p.sku, ''), COALESCE(p.search_key, ''),
		       COALESCE(p.origin_organization_id::text, ''),
		       COALESCE(p.cost_price, 0), COALESCE(p.list_price, 0)
		FROM products p
	`
	args := []interface{}{tenantID, key}
	switch {
	case orgFilter != nil:
		// INNER JOIN — product must be assigned to this org.
		query += `
		INNER JOIN product_organization_settings pos
		        ON pos.product_id = p.id AND pos.organization_id = $3
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL
		  AND upper(p.search_key) = upper($2)`
		args = append(args, *orgFilter)
	case orgExclude != nil:
		// Any org EXCEPT this one. Use EXISTS so a product linked to
		// multiple orgs (one of them the excluded) still matches via
		// one of the others.
		query += `
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL
		  AND upper(p.search_key) = upper($2)
		  AND EXISTS (
		      SELECT 1 FROM product_organization_settings pos
		      WHERE pos.product_id = p.id AND pos.organization_id <> $3
		  )`
		args = append(args, *orgExclude)
	default:
		query += `
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL
		  AND upper(p.search_key) = upper($2)`
	}
	query += ` ORDER BY p.name LIMIT 50`

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("search by key failed", "error", err)
		response.InternalError(c, "Failed to search products")
		return
	}
	defer rows.Close()

	type match struct {
		ID             string  `json:"id"`
		Name           string  `json:"name"`
		Code           string  `json:"code"`
		SKU            string  `json:"sku"`
		SearchKey      string  `json:"search_key"`
		OrganizationID string  `json:"organization_id"`
		CostPrice      float64 `json:"cost_price"`
		ListPrice      float64 `json:"list_price"`
	}
	var out []match
	for rows.Next() {
		var m match
		if err := rows.Scan(&m.ID, &m.Name, &m.Code, &m.SKU, &m.SearchKey,
			&m.OrganizationID, &m.CostPrice, &m.ListPrice); err != nil {
			continue
		}
		out = append(out, m)
	}
	response.Success(c, gin.H{"products": out})
}

// lookupSearchKeyForName finds an existing product in the same tenant
// (any organisation) whose name matches `name` and has a non-empty
// search_key, and returns that key. Used by the smeta importer to
// auto-copy a key when the same named product already exists
// elsewhere in the tenant — so two organisations that both call the
// material "Цемент М400" automatically share a key without a human
// clicking "Generate" twice.
//
// Returns "" if no suitable source product is found.
func (h *Handler) lookupSearchKeyForName(tenantID uuid.UUID, name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	var key string
	err := h.db.QueryRow(`
		SELECT search_key
		FROM products
		WHERE tenant_id = $1 AND deleted_at IS NULL
		  AND search_key IS NOT NULL AND search_key <> ''
		  AND lower(name) = lower($2)
		ORDER BY created_at ASC
		LIMIT 1`,
		tenantID, name,
	).Scan(&key)
	if err != nil {
		return ""
	}
	return key
}
