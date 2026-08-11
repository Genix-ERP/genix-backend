package handler

import (
	"database/sql"
	"math"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// ============================================================================
// /admin/inventory-reconcile
//
// PURPOSE
// Side-by-side comparison of the inventory MODULE value (sum of physical
// qty × cost from the `inventory` + `products` tables) vs. the inventory
// GL ACCOUNT balance (current_balance on the matched 1xxx/2xxx asset
// leaf), broken down per (tenant, organization). Helps the system admin
// answer the question:
//
//   "The Ombor stat card shows X but Buxgalteriya Xom ashyo shows Y —
//    where is the gap?"
//
// Implementation notes:
//   * The physical formula matches the one used by migration 410 and by
//     the frontend `getInventorySummary()` helper:
//         SUM(quantity_on_hand × COALESCE(NULLIF(unit_cost, 0),
//                                         product.cost_price, 0))
//     so the number returned here is comparable apples-to-apples with the
//     "Jami qiymat" card in the inventory module.
//   * The GL side considers the same code preference list used by 410
//     (1030 → 2910 → 2810 → 1020 → 1040 → 1050 → 2010) as the primary
//     inventory leaf. It additionally surfaces *every* asset account whose
//     code starts with 10/13/29/28/20 and whose balance is non-zero, so
//     the admin can spot cases where stock got booked to a sibling
//     account (e.g., 2010 WIP) and is therefore "missing" from a
//     single-account comparison.
//   * The endpoint is read-only and goes through the existing /admin
//     group, which is gated by middleware.RequireSystemAdmin(). No
//     additional permission check needed.
// ============================================================================

// reconcileProduct is one row in the top-products list returned for a
// (tenant, org) pair — the products that contribute the most to the
// physical inventory total. Used to identify "this one product is 80% of
// the gap" type situations.
type reconcileProduct struct {
	ProductID  uuid.UUID `json:"product_id"`
	Code       string    `json:"code"`
	Name       string    `json:"name"`
	QtyOnHand  float64   `json:"quantity_on_hand"`
	UnitCost   float64   `json:"unit_cost_used"` // effective cost used in the SUM
	TotalValue float64   `json:"total_value"`
}

// reconcileAccount is the shape used both for the primary matched
// inventory leaf and for the "all asset accounts in inventory-like code
// ranges" list. Helps surface accidental splits of stock value across
// multiple GL leaves.
type reconcileAccount struct {
	AccountID      uuid.UUID `json:"account_id"`
	Code           string    `json:"code"`
	Name           string    `json:"name"`
	CurrentBalance float64   `json:"current_balance"`
}

// reconcileOrgEntry is the per-(tenant, organization) line in the
// response array. The "difference" field is the headline number — if
// non-zero, the inventory module and the GL disagree on the value of
// stock for that org.
type reconcileOrgEntry struct {
	TenantID       uuid.UUID `json:"tenant_id"`
	TenantName     string    `json:"tenant_name"`
	OrganizationID uuid.UUID `json:"organization_id"`
	OrgName        string    `json:"organization_name"`

	// Physical side — from `inventory` × `products`, same formula as the
	// frontend stat card.
	PhysicalValue float64 `json:"physical_value"`
	ProductCount  int     `json:"product_count"`
	LotCount      int     `json:"lot_count"`

	// GL side — the matched primary inventory leaf, plus every other
	// inventory-like asset leaf with a non-zero balance.
	PrimaryInventoryAccount *reconcileAccount  `json:"primary_inventory_account,omitempty"`
	OtherInventoryAccounts  []reconcileAccount `json:"other_inventory_accounts,omitempty"`
	GLInventorySum          float64            `json:"gl_inventory_sum"`

	// Headline: physical_value - gl_inventory_sum. Positive = stock
	// exists in module but isn't recognized in GL (likely missing
	// opening balance or under-recognized purchase). Negative = GL has
	// more than the module (likely COGS / consumption not posted, or a
	// duplicate purchase JE).
	Difference float64 `json:"difference"`

	// Did the migration 405/410 opening-balance JE get posted for this
	// pair?
	HasOpeningJE bool   `json:"has_opening_je"`
	OpeningRef   string `json:"opening_je_reference"`

	// Top-N products by physical value.
	TopProducts []reconcileProduct `json:"top_products"`
}

// codeIsInventoryLike matches asset codes that are commonly used to hold
// inventory value under the NAS chart of accounts. We use a SQL
// expression rather than a Go-side filter so the DB can use the index on
// accounts.code if one exists.
const inventoryCodePredicate = `(
	a.code LIKE '10%'   OR  -- raw materials, packaging, spare parts (1010..1099)
	a.code LIKE '13%'   OR  -- legacy 13xx range used in some templates
	a.code LIKE '20%'   OR  -- 2010 WIP, 2050 semi-finished
	a.code LIKE '28%'   OR  -- 2810 finished goods
	a.code LIKE '29%'       -- 2910 goods for sale
)`

// GetInventoryReconcile is the diagnostic endpoint. Returns one row per
// (tenant, organization) pair that owns any inventory, with both sides
// of the comparison and the top contributing products.
//
//	GET /api/v1/admin/inventory-reconcile
//
// Query params:
//
//	top_n       — how many top products to include per org (default 20, max 100)
//	tenant_id   — limit to a single tenant (optional)
//	min_diff    — only include rows where ABS(difference) >= this value
//	              (optional, useful for surfacing problem orgs only)
func (h *Handler) GetInventoryReconcile(c *gin.Context) {
	// Parse query params
	topN := 20
	if t := c.Query("top_n"); t != "" {
		// Cheap and forgiving — bad input falls back to default.
		if v, err := parsePositiveInt(t); err == nil {
			topN = v
		}
		if topN > 100 {
			topN = 100
		}
	}
	minDiff := 0.0
	if md := c.Query("min_diff"); md != "" {
		if v, err := parsePositiveFloat(md); err == nil {
			minDiff = v
		}
	}
	var tenantFilter uuid.UUID
	if tf := c.Query("tenant_id"); tf != "" {
		if id, err := uuid.Parse(tf); err == nil {
			tenantFilter = id
		}
	}

	// ------------------------------------------------------------------
	// Step 1 — discover every (tenant, organization) pair that owns
	// non-zero inventory, along with the physical aggregate.
	//
	// We exclude scrap warehouses to match what the inventory module
	// reports on its dashboard (Inventory.jsx / InventoryManagement.jsx
	// both reflect non-scrap stock).
	// ------------------------------------------------------------------
	pairsQuery := `
		SELECT
			i.tenant_id,
			COALESCE(t.name, '')                                                AS tenant_name,
			w.organization_id,
			COALESCE(o.name, '')                                                AS org_name,
			COALESCE(SUM(i.quantity_on_hand * COALESCE(NULLIF(i.unit_cost, 0),
			                                          p.cost_price, 0)), 0)     AS physical_value,
			COUNT(DISTINCT p.id)                                                AS product_count,
			COUNT(*)                                                            AS lot_count
		FROM inventory i
		JOIN warehouses    w ON w.id = i.warehouse_id
		JOIN products      p ON p.id = i.product_id AND p.deleted_at IS NULL
		LEFT JOIN tenants  t ON t.id = i.tenant_id
		LEFT JOIN organizations o ON o.id = w.organization_id
		WHERE w.organization_id IS NOT NULL
		  AND COALESCE(w.warehouse_type, 'regular') <> 'scrap'
		  AND i.quantity_on_hand > 0
	`
	args := []interface{}{}
	if tenantFilter != uuid.Nil {
		pairsQuery += " AND i.tenant_id = $1"
		args = append(args, tenantFilter)
	}
	pairsQuery += `
		GROUP BY i.tenant_id, t.name, w.organization_id, o.name
		ORDER BY physical_value DESC
	`

	rows, err := h.db.Query(pairsQuery, args...)
	if err != nil {
		h.log.Error("inventory_reconcile: pairs query failed", "error", err)
		response.InternalError(c, "Failed to query inventory pairs")
		return
	}
	defer rows.Close()

	results := make([]reconcileOrgEntry, 0)
	for rows.Next() {
		var e reconcileOrgEntry
		if err := rows.Scan(
			&e.TenantID, &e.TenantName,
			&e.OrganizationID, &e.OrgName,
			&e.PhysicalValue, &e.ProductCount, &e.LotCount,
		); err != nil {
			h.log.Error("inventory_reconcile: pairs scan failed", "error", err)
			continue
		}
		results = append(results, e)
	}
	rows.Close()

	// ------------------------------------------------------------------
	// Step 2 — for each pair, enrich with: GL accounts, opening-JE
	// flag, top products.
	// ------------------------------------------------------------------
	for idx := range results {
		entry := &results[idx]

		// 2a — every inventory-like asset leaf for this org, ordered by
		// the same priority list migration 410 uses for picking the
		// primary inventory account.
		acctQuery := `
			SELECT a.id, a.code, a.name, COALESCE(a.current_balance, 0)
			FROM accounts a
			JOIN account_types at ON at.id = a.account_type_id
			WHERE a.tenant_id = $1
			  AND a.organization_id = $2
			  AND a.deleted_at IS NULL
			  AND COALESCE(a.is_leaf, true) = true
			  AND at.category = 'asset'
			  AND ` + inventoryCodePredicate + `
			ORDER BY CASE a.code
				WHEN '1030' THEN 1
				WHEN '2910' THEN 2
				WHEN '2810' THEN 3
				WHEN '1020' THEN 4
				WHEN '1040' THEN 5
				WHEN '1050' THEN 6
				WHEN '2010' THEN 7
				ELSE 99
			END, a.code
		`
		acctRows, err := h.db.Query(acctQuery, entry.TenantID, entry.OrganizationID)
		if err != nil {
			h.log.Error("inventory_reconcile: accounts query failed",
				"error", err, "tenant", entry.TenantID, "org", entry.OrganizationID)
			continue
		}
		var allAccts []reconcileAccount
		for acctRows.Next() {
			var a reconcileAccount
			if err := acctRows.Scan(&a.AccountID, &a.Code, &a.Name, &a.CurrentBalance); err != nil {
				h.log.Error("inventory_reconcile: account scan failed", "error", err)
				continue
			}
			allAccts = append(allAccts, a)
		}
		acctRows.Close()

		// Primary = first match by priority (the same one migration 410
		// would book the opening balance to). The rest go into
		// "other" so the admin can see split-account scenarios.
		if len(allAccts) > 0 {
			primary := allAccts[0]
			entry.PrimaryInventoryAccount = &primary
			if len(allAccts) > 1 {
				entry.OtherInventoryAccounts = allAccts[1:]
			}
		}
		// GL sum = ALL inventory-like leaves, because in practice stock
		// value can legitimately end up split (1030 raw + 2810 finished
		// + 2010 WIP). This is the most-honest comparator vs. the
		// physical module total.
		for _, a := range allAccts {
			entry.GLInventorySum += a.CurrentBalance
		}
		entry.Difference = entry.PhysicalValue - entry.GLInventorySum

		// 2b — opening-balance JE flag (migration 405 / 410 reference
		// anchor format).
		entry.OpeningRef = "INV-OPENING-" + entry.TenantID.String() + "-" + entry.OrganizationID.String()
		var existsCount int
		_ = h.db.QueryRow(`
			SELECT COUNT(*)
			FROM journal_entries
			WHERE reference = $1
			  AND deleted_at IS NULL
		`, entry.OpeningRef).Scan(&existsCount)
		entry.HasOpeningJE = existsCount > 0

		// 2c — top-N products by physical value. Same formula as the
		// org-level aggregate, just grouped by product so the admin can
		// see which products dominate.
		topQuery := `
			SELECT p.id, COALESCE(p.code, ''), COALESCE(p.name, ''),
			       COALESCE(SUM(i.quantity_on_hand), 0)                                   AS qty,
			       CASE WHEN COALESCE(SUM(i.quantity_on_hand), 0) = 0 THEN 0
			            ELSE COALESCE(SUM(i.quantity_on_hand * COALESCE(NULLIF(i.unit_cost, 0),
			                                                          p.cost_price, 0)), 0)
			                 / NULLIF(SUM(i.quantity_on_hand), 0)
			       END                                                                    AS unit_cost,
			       COALESCE(SUM(i.quantity_on_hand * COALESCE(NULLIF(i.unit_cost, 0),
			                                                p.cost_price, 0)), 0)         AS total_value
			FROM inventory i
			JOIN warehouses w ON w.id = i.warehouse_id
			JOIN products   p ON p.id = i.product_id AND p.deleted_at IS NULL
			WHERE i.tenant_id = $1
			  AND w.organization_id = $2
			  AND COALESCE(w.warehouse_type, 'regular') <> 'scrap'
			  AND i.quantity_on_hand > 0
			GROUP BY p.id, p.code, p.name
			ORDER BY total_value DESC, p.id ASC
			LIMIT $3
		`
		prodRows, err := h.db.Query(topQuery, entry.TenantID, entry.OrganizationID, topN)
		if err != nil {
			h.log.Error("inventory_reconcile: top-products query failed",
				"error", err, "tenant", entry.TenantID, "org", entry.OrganizationID)
			continue
		}
		for prodRows.Next() {
			var p reconcileProduct
			var unitCost sql.NullFloat64
			if err := prodRows.Scan(&p.ProductID, &p.Code, &p.Name, &p.QtyOnHand, &unitCost, &p.TotalValue); err != nil {
				h.log.Error("inventory_reconcile: top-products scan failed", "error", err)
				continue
			}
			if unitCost.Valid {
				p.UnitCost = unitCost.Float64
			}
			entry.TopProducts = append(entry.TopProducts, p)
		}
		prodRows.Close()
	}

	// ------------------------------------------------------------------
	// Step 3 — apply min_diff filter (optional) and aggregate totals.
	// ------------------------------------------------------------------
	filtered := make([]reconcileOrgEntry, 0, len(results))
	var totalPhysical, totalGL float64
	for _, r := range results {
		totalPhysical += r.PhysicalValue
		totalGL += r.GLInventorySum
		if minDiff > 0 && math.Abs(r.Difference) < minDiff {
			continue
		}
		filtered = append(filtered, r)
	}

	response.Success(c, gin.H{
		"count":              len(filtered),
		"total_physical":     totalPhysical,
		"total_gl_inventory": totalGL,
		"total_difference":   totalPhysical - totalGL,
		"top_n":              topN,
		"min_diff_filter":    minDiff,
		"results":            filtered,
	})
}

// Tiny helpers — kept private to this file. We avoid pulling in strconv at
// the top of the file by routing through these so the parsing intent is
// clearer at the call sites.

func parsePositiveInt(s string) (int, error) {
	var n int
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, errBadNumber
		}
		n = n*10 + int(ch-'0')
		if n > 1_000_000 {
			return 0, errBadNumber
		}
	}
	if n <= 0 {
		return 0, errBadNumber
	}
	return n, nil
}

func parsePositiveFloat(s string) (float64, error) {
	var (
		intPart, fracPart float64
		fracDiv           = 1.0
		sawDot            bool
	)
	for _, ch := range s {
		if ch == '.' {
			if sawDot {
				return 0, errBadNumber
			}
			sawDot = true
			continue
		}
		if ch < '0' || ch > '9' {
			return 0, errBadNumber
		}
		if !sawDot {
			intPart = intPart*10 + float64(ch-'0')
		} else {
			fracPart = fracPart*10 + float64(ch-'0')
			fracDiv *= 10
		}
	}
	v := intPart + fracPart/fracDiv
	if v < 0 {
		return 0, errBadNumber
	}
	return v, nil
}

// errBadNumber is sentinel for the tiny parsers above — they only need to
// distinguish "ok" from "fall back to default", so a single error value
// is enough.
var errBadNumber = &reconcileError{"bad number"}

type reconcileError struct{ msg string }

func (e *reconcileError) Error() string { return e.msg }
