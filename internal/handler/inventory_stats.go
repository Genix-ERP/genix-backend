package handler

// GET /inventory/stats — the single call behind the rebuilt Ombor "Asosiy
// panel" (docs/ombor-audit.md §8): stat tiles, the 6-month value dynamics
// chart, the per-category value chart, the low-stock mini-table and the
// recent-movements feed. One endpoint so the dashboard needs one request
// (same shape of contract as GET /expenses/stats from the Xarajatlar
// rebuild) instead of hydrating 15k context rows client-side.
//
// Conventions this endpoint fixes in one place:
//   - Value formula: qty × COALESCE(NULLIF(inventory.unit_cost,0),
//     products.cost_price, 0) — same as the admin reconcile endpoint, so
//     "Umumiy qiymat" finally agrees with Buxgalteriya reconciliation.
//   - Low-stock threshold: reorder_point if set, else min_stock_level —
//     the same rule the workflow scheduler uses (workflow_rules.go), ending
//     the three-formula disagreement between dashboard, director panel and
//     alerts.
//   - History comes from the stock_ledger view (migration 447), not from a
//     client-side movement dump.
//
// Scrap warehouses are excluded from all value figures.

import (
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const invValueExpr = `i.quantity_on_hand * COALESCE(NULLIF(i.unit_cost, 0), p.cost_price, 0)`

// lowStockThresholdExpr is THE low-stock rule. Keep in sync with
// checkInventoryThresholds (workflow_rules.go).
const lowStockThresholdExpr = `CASE WHEN COALESCE(p.reorder_point, 0) > 0 THEN p.reorder_point ELSE COALESCE(p.min_stock_level, 0) END`

func (h *Handler) GetInventoryStats(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	var orgArg interface{}
	if orgID, okOrg := middleware.GetOrganizationID(c); okOrg && orgID != uuid.Nil {
		orgArg = orgID
	}

	// ── Totals ──────────────────────────────────────────────────────────
	var totalValue float64
	var lowStockCount, productCount, warehouseCount, todayMovements int

	h.db.QueryRow(`
		SELECT COALESCE(SUM(`+invValueExpr+`), 0)
		FROM inventory i
		JOIN warehouses w ON w.id = i.warehouse_id AND w.deleted_at IS NULL
			AND COALESCE(w.warehouse_type, 'regular') != 'scrap'
		LEFT JOIN products p ON p.id = i.product_id AND p.deleted_at IS NULL
		WHERE i.tenant_id = $1
		  AND ($2::uuid IS NULL OR w.organization_id = $2)
	`, tenantID, orgArg).Scan(&totalValue)

	h.db.QueryRow(`
		SELECT COUNT(*) FROM (
			SELECT p.id
			FROM products p
			LEFT JOIN inventory i ON i.product_id = p.id AND i.tenant_id = p.tenant_id
			LEFT JOIN warehouses w ON w.id = i.warehouse_id
			WHERE p.tenant_id = $1 AND p.deleted_at IS NULL AND p.is_active = true
			  AND p.track_inventory = true
			  AND ($2::uuid IS NULL OR w.organization_id IS NULL OR w.organization_id = $2)
			GROUP BY p.id, p.reorder_point, p.min_stock_level
			HAVING `+lowStockThresholdExpr+` > 0
			   AND COALESCE(SUM(i.quantity_on_hand), 0) <= `+lowStockThresholdExpr+`
		) low
	`, tenantID, orgArg).Scan(&lowStockCount)

	h.db.QueryRow(`
		SELECT COUNT(*) FROM products
		WHERE tenant_id = $1 AND deleted_at IS NULL AND is_active = true
	`, tenantID).Scan(&productCount)

	h.db.QueryRow(`
		SELECT COUNT(*) FROM warehouses
		WHERE tenant_id = $1 AND deleted_at IS NULL AND is_active = true
		  AND ($2::uuid IS NULL OR organization_id = $2)
	`, tenantID, orgArg).Scan(&warehouseCount)

	h.db.QueryRow(`
		SELECT COUNT(*) FROM stock_ledger
		WHERE tenant_id = $1 AND transaction_date::date = CURRENT_DATE
		  AND ($2::uuid IS NULL OR organization_id = $2)
	`, tenantID, orgArg).Scan(&todayMovements)

	// ── Value dynamics, last 6 months ───────────────────────────────────
	// Month-end values are reconstructed backwards from the CURRENT value
	// by subtracting the ledger's value deltas month by month — the balance
	// table has no history, the ledger does.
	type monthPoint struct {
		Month string  `json:"month"`
		Value float64 `json:"value"`
	}
	const monthsBack = 6
	deltas := make(map[string]float64)
	dRows, dErr := h.db.Query(`
		SELECT to_char(date_trunc('month', transaction_date), 'YYYY-MM') AS m,
		       COALESCE(SUM(qty_delta * COALESCE(unit_cost, 0)), 0)
		FROM stock_ledger
		WHERE tenant_id = $1
		  AND transaction_date >= date_trunc('month', CURRENT_DATE) - INTERVAL '5 months'
		  AND ($2::uuid IS NULL OR organization_id = $2)
		GROUP BY 1
	`, tenantID, orgArg)
	if dErr == nil {
		for dRows.Next() {
			var m string
			var v float64
			if dRows.Scan(&m, &v) == nil {
				deltas[m] = v
			}
		}
		dRows.Close()
	}
	valueSeries := make([]monthPoint, monthsBack)
	nowT := time.Now()
	// Anchor to day 1 before stepping months: AddDate from the 31st
	// normalizes "Feb 31" to March, so the same month appeared twice and
	// another vanished — and its delta was subtracted twice.
	monthAnchor := time.Date(nowT.Year(), nowT.Month(), 1, 0, 0, 0, 0, nowT.Location())
	running := totalValue
	for i := 0; i < monthsBack; i++ {
		m := monthAnchor.AddDate(0, -i, 0)
		key := m.Format("2006-01")
		valueSeries[monthsBack-1-i] = monthPoint{Month: key, Value: running}
		running -= deltas[key] // stepping back past month i removes its delta
	}

	// ── Value by category ───────────────────────────────────────────────
	type catValue struct {
		Name  string  `json:"name"`
		Value float64 `json:"value"`
	}
	categoryValues := []catValue{}
	cRows, cErr := h.db.Query(`
		SELECT COALESCE(pc.name, 'Boshqa') AS cat, COALESCE(SUM(`+invValueExpr+`), 0) AS v
		FROM inventory i
		JOIN warehouses w ON w.id = i.warehouse_id AND w.deleted_at IS NULL
			AND COALESCE(w.warehouse_type, 'regular') != 'scrap'
		LEFT JOIN products p ON p.id = i.product_id AND p.deleted_at IS NULL
		LEFT JOIN product_categories pc ON pc.id = p.category_id
		WHERE i.tenant_id = $1 AND i.quantity_on_hand > 0
		  AND ($2::uuid IS NULL OR w.organization_id = $2)
		GROUP BY 1
		HAVING COALESCE(SUM(`+invValueExpr+`), 0) > 0
		ORDER BY v DESC
	`, tenantID, orgArg)
	if cErr == nil {
		for cRows.Next() {
			var cv catValue
			if cRows.Scan(&cv.Name, &cv.Value) == nil {
				categoryValues = append(categoryValues, cv)
			}
		}
		cRows.Close()
	}
	if len(categoryValues) > 6 {
		other := 0.0
		for _, cv := range categoryValues[6:] {
			other += cv.Value
		}
		categoryValues = append(categoryValues[:6], catValue{Name: "Boshqa", Value: other})
	}

	// ── Low-stock items (top 10 by severity) ────────────────────────────
	type lowItem struct {
		ProductID  uuid.UUID `json:"product_id"`
		Name       string    `json:"name"`
		Code       string    `json:"code"`
		OnHand     float64   `json:"on_hand"`
		Threshold  float64   `json:"threshold"`
		ReorderQty float64   `json:"reorder_qty"`
	}
	lowItems := []lowItem{}
	lRows, lErr := h.db.Query(`
		SELECT p.id, p.name, COALESCE(p.code, ''),
		       COALESCE(SUM(i.quantity_on_hand), 0) AS on_hand,
		       `+lowStockThresholdExpr+` AS threshold,
		       COALESCE(p.reorder_quantity, 0)
		FROM products p
		LEFT JOIN inventory i ON i.product_id = p.id AND i.tenant_id = p.tenant_id
		LEFT JOIN warehouses w ON w.id = i.warehouse_id
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL AND p.is_active = true
		  AND p.track_inventory = true
		  AND ($2::uuid IS NULL OR w.organization_id IS NULL OR w.organization_id = $2)
		GROUP BY p.id, p.name, p.code, p.reorder_point, p.min_stock_level, p.reorder_quantity
		HAVING `+lowStockThresholdExpr+` > 0
		   AND COALESCE(SUM(i.quantity_on_hand), 0) <= `+lowStockThresholdExpr+`
		ORDER BY COALESCE(SUM(i.quantity_on_hand), 0) / `+lowStockThresholdExpr+` ASC
		LIMIT 10
	`, tenantID, orgArg)
	if lErr == nil {
		for lRows.Next() {
			var li lowItem
			if lRows.Scan(&li.ProductID, &li.Name, &li.Code, &li.OnHand, &li.Threshold, &li.ReorderQty) == nil {
				lowItems = append(lowItems, li)
			}
		}
		lRows.Close()
	}

	// ── Recent movements (last 10 ledger rows) ──────────────────────────
	type recentMove struct {
		ID          uuid.UUID `json:"id"`
		Type        string    `json:"type"`
		RefType     string    `json:"reference_type"`
		ProductName string    `json:"product_name"`
		Warehouse   string    `json:"warehouse_name"`
		Qty         float64   `json:"qty"`
		TotalCost   float64   `json:"total_cost"`
		Date        time.Time `json:"date"`
	}
	recentMoves := []recentMove{}
	rRows, rErr := h.db.Query(`
		SELECT sl.id, sl.transaction_type, COALESCE(sl.reference_type, ''),
		       COALESCE(p.name, '—'), COALESCE(w.name, '—'),
		       sl.qty_delta, COALESCE(sl.total_cost, 0), sl.transaction_date
		FROM stock_ledger sl
		LEFT JOIN products p ON p.id = sl.product_id
		LEFT JOIN warehouses w ON w.id = sl.warehouse_id
		WHERE sl.tenant_id = $1
		  AND ($2::uuid IS NULL OR sl.organization_id = $2)
		ORDER BY sl.transaction_date DESC, sl.created_at DESC
		LIMIT 10
	`, tenantID, orgArg)
	if rErr == nil {
		for rRows.Next() {
			var rm recentMove
			if rRows.Scan(&rm.ID, &rm.Type, &rm.RefType, &rm.ProductName, &rm.Warehouse, &rm.Qty, &rm.TotalCost, &rm.Date) == nil {
				recentMoves = append(recentMoves, rm)
			}
		}
		rRows.Close()
	}

	response.Success(c, gin.H{
		"totals": gin.H{
			"total_value":     totalValue,
			"low_stock_count": lowStockCount,
			"product_count":   productCount,
			"warehouse_count": warehouseCount,
			"today_movements": todayMovements,
		},
		"value_series":    valueSeries,
		"category_values": categoryValues,
		"low_stock_items": lowItems,
		"recent_moves":    recentMoves,
	})
}
