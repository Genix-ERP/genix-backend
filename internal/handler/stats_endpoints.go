package handler

// Aggregate ("stats") endpoints the mobile client already calls but which were
// never registered, so each one 404s or falls into a `/:id` route and 400s — the
// screens above them render zeros or an empty state.
//
//	GET /accounts/summary     — per-category balance totals (the 5 KPI cards)
//	GET /inventory/lots/stats — the lot KPI strip
//
// Both are read-only, tenant- and organization-scoped, and aggregate in SQL: a
// KPI must never be derived from a paginated page.
//
// Note: /expenses/stats and /assets/:id/entries were also on the spec's P0 list
// but already exist here (expense.go GetExpenseStats, fa_extras.go
// GetAssetEntries) — that document was written against an older checkout.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// GetAccountsSummary returns chart-of-accounts balances grouped by category.
// GET /accounts/summary?search=&include_inactive=&organization_id=
//
// The five keys are always present (0 when empty) — the client reads them as
// numbers. Signs match `current_balance` exactly as ListAccounts returns it; do
// NOT normalise by normal_balance here, or the cards stop matching the list.
func (h *Handler) GetAccountsSummary(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Mirror ListAccounts' filters exactly, or the cards disagree with the list.
	where := " WHERE a.tenant_id = $1 AND a.deleted_at IS NULL"
	args := []interface{}{tenantID}
	argCount := 1

	orgID := c.Query("organization_id")
	if orgID == "" {
		if oid, okOrg := middleware.GetOrganizationID(c); okOrg && oid != uuid.Nil {
			orgID = oid.String()
		}
	}
	if orgID != "" {
		argCount++
		where += fmt.Sprintf(" AND a.organization_id = $%d", argCount)
		args = append(args, orgID)
	}
	if c.Query("include_inactive") != "true" {
		where += " AND a.is_active = true"
	}
	if search := c.Query("search"); search != "" {
		argCount++
		where += fmt.Sprintf(" AND (a.code ILIKE $%d OR a.name ILIKE $%d OR a.name_uz ILIKE $%d)",
			argCount, argCount, argCount)
		args = append(args, "%"+search+"%")
	}

	rows, err := h.db.Query(`
		SELECT at.category, COALESCE(SUM(a.current_balance), 0)
		FROM accounts a
		JOIN account_types at ON a.account_type_id = at.id`+where+`
		GROUP BY at.category`, args...)
	if err != nil {
		h.log.Error("Failed to build accounts summary", "error", err)
		response.InternalError(c, "Failed to build accounts summary")
		return
	}
	defer rows.Close()

	// All five always emitted, 0 when the tenant has no accounts of that kind.
	out := gin.H{"asset": 0.0, "liability": 0.0, "equity": 0.0, "revenue": 0.0, "expense": 0.0}
	for rows.Next() {
		var category string
		var total float64
		if rows.Scan(&category, &total) == nil {
			out[strings.ToLower(category)] = total
		}
	}
	response.Success(c, out)
}

// GetInventoryLotStats returns the lot KPI strip.
// GET /inventory/lots/stats?warehouse_id=&expiry_days=30&low_stock_threshold_percent=20
//
// MUST be registered before /lots/:id, or "stats" is parsed as a lot UUID and
// the request 400s (which is exactly what happened before this landed).
func (h *Handler) GetInventoryLotStats(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	expiryDays := 30
	if v, e := strconv.Atoi(c.Query("expiry_days")); e == nil && v > 0 {
		expiryDays = v
	}
	lowPct := 20.0
	if v, e := strconv.ParseFloat(c.Query("low_stock_threshold_percent"), 64); e == nil && v > 0 {
		lowPct = v
	}

	// Scope identically to ListInventoryLots, or the strip disagrees with the
	// list it sits above.
	where := " WHERE il.tenant_id = $1"
	args := []interface{}{tenantID}
	argCount := 1
	if oid, okOrg := middleware.GetOrganizationID(c); okOrg && oid != uuid.Nil {
		argCount++
		where += fmt.Sprintf(" AND w.organization_id = $%d", argCount)
		args = append(args, oid)
	}
	if v := c.Query("warehouse_id"); v != "" {
		if wid, e := uuid.Parse(v); e == nil {
			argCount++
			where += fmt.Sprintf(" AND il.warehouse_id = $%d", argCount)
			args = append(args, wid)
		}
	}
	argCount++
	daysArg := argCount
	args = append(args, expiryDays)
	argCount++
	pctArg := argCount
	args = append(args, lowPct)

	var activeLots, expiringSoon, lowStock int
	var totalValue float64
	err := h.db.QueryRow(fmt.Sprintf(`
		SELECT COUNT(*) FILTER (WHERE il.status = 'active'),
		       COUNT(*) FILTER (WHERE il.status = 'active' AND il.expiry_date IS NOT NULL
		                          AND il.expiry_date <= CURRENT_DATE + ($%d::int * INTERVAL '1 day')),
		       COALESCE(SUM(il.remaining_quantity * il.unit_cost) FILTER (WHERE il.status = 'active'), 0),
		       COUNT(*) FILTER (WHERE il.status = 'active' AND il.initial_quantity > 0
		                          AND il.remaining_quantity <= il.initial_quantity * ($%d::numeric / 100))
		FROM inventory_lots il
		JOIN warehouses w ON w.id = il.warehouse_id`+where, daysArg, pctArg), args...).
		Scan(&activeLots, &expiringSoon, &totalValue, &lowStock)
	if err != nil {
		h.log.Error("Failed to build lot stats", "error", err)
		response.InternalError(c, "Failed to build lot stats")
		return
	}

	response.Success(c, gin.H{
		"active_lots":                 activeLots,
		"expiring_soon":               expiringSoon,
		"expiring_days":               expiryDays,
		"total_value":                 totalValue,
		"low_stock_count":             lowStock,
		"low_stock_threshold_percent": lowPct,
	})
}
