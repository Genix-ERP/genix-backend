package handler

// GET /purchase-orders/stats — the single call behind the rebuilt Xarid
// "Asosiy panel" (docs/xarid-audit.md §11.7): stat tiles, the 6-month
// purchase dynamics chart, the top-suppliers chart, the recent-orders feed
// and the upcoming-deliveries mini-table. Same shape of contract as
// GET /inventory/stats (Ombor) and GET /expenses/stats (Xarajatlar) —
// the dashboard makes one request instead of hydrating the whole PO list
// client-side.
//
// Conventions fixed in one place:
//   - "Buyurtmalar" counts POs by order_date within [from, to], excluding
//     cancelled and soft-deleted rows.
//   - "Kutilmoqda" = confirmed but not fully received (approved | ordered |
//     partial), regardless of period — it is an open-pipeline figure.
//   - "Kechikkan" = open POs whose expected_date is in the past — the same
//     rule the purchase_order.delivery_overdue scheduler uses.
//   - "Yetkazib beruvchilarga qarz" comes from posted GL lines on the 6010
//     account (credit − debit), the akt-sverka source of truth — NOT from
//     contacts.current_balance.

import (
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// openPOStatuses is THE "confirmed but not fully received" rule. Keep in
// sync with checkOverduePurchaseDeliveries (workflow_rules.go).
const openPOStatuses = `('approved', 'ordered', 'partial')`

func (h *Handler) GetPurchaseOrderStats(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	var orgArg interface{}
	if orgID, okOrg := middleware.GetOrganizationID(c); okOrg && orgID != uuid.Nil {
		orgArg = orgID
	}

	// Period: [from, to] on order_date; defaults to the current month.
	nowT := time.Now()
	from := time.Date(nowT.Year(), nowT.Month(), 1, 0, 0, 0, 0, nowT.Location())
	to := nowT
	if s := c.Query("from"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			from = t
		}
	}
	if s := c.Query("to"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			// inclusive end of day
			to = t.Add(24*time.Hour - time.Microsecond)
		}
	}

	// ── Totals ──────────────────────────────────────────────────────────
	var ordersCount int
	var ordersSum float64
	h.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(total_amount), 0)
		FROM purchase_orders
		WHERE tenant_id = $1 AND deleted_at IS NULL AND status != 'cancelled'
		  AND order_date >= $2 AND order_date <= $3
		  AND ($4::uuid IS NULL OR organization_id = $4)
	`, tenantID, from, to, orgArg).Scan(&ordersCount, &ordersSum)

	var pendingCount int
	var pendingSum float64
	h.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(total_amount), 0)
		FROM purchase_orders
		WHERE tenant_id = $1 AND deleted_at IS NULL AND status IN `+openPOStatuses+`
		  AND ($2::uuid IS NULL OR organization_id = $2)
	`, tenantID, orgArg).Scan(&pendingCount, &pendingSum)

	var overdueCount int
	h.db.QueryRow(`
		SELECT COUNT(*)
		FROM purchase_orders
		WHERE tenant_id = $1 AND deleted_at IS NULL AND status IN `+openPOStatuses+`
		  AND expected_date IS NOT NULL AND expected_date < CURRENT_DATE
		  AND ($2::uuid IS NULL OR organization_id = $2)
	`, tenantID, orgArg).Scan(&overdueCount)

	// Supplier AP from posted GL lines on 6010 (liability: credit − debit).
	var payableTotal float64
	h.db.QueryRow(`
		SELECT COALESCE(SUM(jel.credit_amount - jel.debit_amount), 0)
		FROM journal_entry_lines jel
		JOIN journal_entries je ON je.id = jel.journal_entry_id AND je.status = 'posted'
		JOIN accounts a ON a.id = jel.account_id
		WHERE je.tenant_id = $1 AND a.code = '6010'
		  AND ($2::uuid IS NULL OR je.organization_id = $2)
	`, tenantID, orgArg).Scan(&payableTotal)

	// ── Purchase dynamics, last 6 months (by order_date) ────────────────
	type monthPoint struct {
		Month string  `json:"month"`
		Value float64 `json:"value"`
		Count int     `json:"count"`
	}
	const monthsBack = 6
	byMonth := map[string]monthPoint{}
	mRows, mErr := h.db.Query(`
		SELECT to_char(date_trunc('month', order_date), 'YYYY-MM') AS m,
		       COALESCE(SUM(total_amount), 0), COUNT(*)
		FROM purchase_orders
		WHERE tenant_id = $1 AND deleted_at IS NULL AND status != 'cancelled'
		  AND order_date >= date_trunc('month', CURRENT_DATE) - INTERVAL '5 months'
		  AND ($2::uuid IS NULL OR organization_id = $2)
		GROUP BY 1
	`, tenantID, orgArg)
	if mErr == nil {
		for mRows.Next() {
			var mp monthPoint
			if mRows.Scan(&mp.Month, &mp.Value, &mp.Count) == nil {
				byMonth[mp.Month] = mp
			}
		}
		mRows.Close()
	}
	monthlySeries := make([]monthPoint, monthsBack)
	// Day-1 anchor — see inventory_stats.go: stepping months from the 31st
	// duplicates one month and drops another.
	monthAnchor := time.Date(nowT.Year(), nowT.Month(), 1, 0, 0, 0, 0, nowT.Location())
	for i := 0; i < monthsBack; i++ {
		key := monthAnchor.AddDate(0, -i, 0).Format("2006-01")
		mp := byMonth[key]
		mp.Month = key
		monthlySeries[monthsBack-1-i] = mp
	}

	// ── Top suppliers by period spend (top 6 + Boshqa) ──────────────────
	type supplierSpend struct {
		VendorID uuid.UUID `json:"vendor_id"`
		Name     string    `json:"name"`
		Value    float64   `json:"value"`
	}
	topSuppliers := []supplierSpend{}
	sRows, sErr := h.db.Query(`
		SELECT po.vendor_id, COALESCE(ct.name, '—'), COALESCE(SUM(po.total_amount), 0) AS v
		FROM purchase_orders po
		LEFT JOIN contacts ct ON ct.id = po.vendor_id
		WHERE po.tenant_id = $1 AND po.deleted_at IS NULL AND po.status != 'cancelled'
		  AND po.order_date >= $2 AND po.order_date <= $3
		  AND ($4::uuid IS NULL OR po.organization_id = $4)
		GROUP BY po.vendor_id, ct.name
		HAVING COALESCE(SUM(po.total_amount), 0) > 0
		ORDER BY v DESC
	`, tenantID, from, to, orgArg)
	if sErr == nil {
		for sRows.Next() {
			var ss supplierSpend
			if sRows.Scan(&ss.VendorID, &ss.Name, &ss.Value) == nil {
				topSuppliers = append(topSuppliers, ss)
			}
		}
		sRows.Close()
	}
	if len(topSuppliers) > 6 {
		other := 0.0
		for _, ss := range topSuppliers[6:] {
			other += ss.Value
		}
		topSuppliers = append(topSuppliers[:6], supplierSpend{Name: "Boshqa", Value: other})
	}

	// ── Recent orders (last 10) ─────────────────────────────────────────
	type recentOrder struct {
		ID          uuid.UUID `json:"id"`
		Number      string    `json:"order_number"`
		VendorName  string    `json:"vendor_name"`
		Status      string    `json:"status"`
		TotalAmount float64   `json:"total_amount"`
		OrderDate   time.Time `json:"order_date"`
	}
	recentOrders := []recentOrder{}
	rRows, rErr := h.db.Query(`
		SELECT po.id, po.order_number, COALESCE(ct.name, '—'), po.status,
		       COALESCE(po.total_amount, 0), po.order_date
		FROM purchase_orders po
		LEFT JOIN contacts ct ON ct.id = po.vendor_id
		WHERE po.tenant_id = $1 AND po.deleted_at IS NULL
		  AND ($2::uuid IS NULL OR po.organization_id = $2)
		ORDER BY po.created_at DESC
		LIMIT 10
	`, tenantID, orgArg)
	if rErr == nil {
		for rRows.Next() {
			var ro recentOrder
			if rRows.Scan(&ro.ID, &ro.Number, &ro.VendorName, &ro.Status, &ro.TotalAmount, &ro.OrderDate) == nil {
				recentOrders = append(recentOrders, ro)
			}
		}
		rRows.Close()
	}

	// ── Upcoming deliveries (open POs by expected_date, overdue first) ──
	type upcomingDelivery struct {
		ID           uuid.UUID `json:"id"`
		Number       string    `json:"order_number"`
		VendorName   string    `json:"vendor_name"`
		Status       string    `json:"status"`
		TotalAmount  float64   `json:"total_amount"`
		ExpectedDate time.Time `json:"expected_date"`
		Overdue      bool      `json:"overdue"`
	}
	upcoming := []upcomingDelivery{}
	uRows, uErr := h.db.Query(`
		SELECT po.id, po.order_number, COALESCE(ct.name, '—'), po.status,
		       COALESCE(po.total_amount, 0), po.expected_date,
		       (po.expected_date < CURRENT_DATE) AS overdue
		FROM purchase_orders po
		LEFT JOIN contacts ct ON ct.id = po.vendor_id
		WHERE po.tenant_id = $1 AND po.deleted_at IS NULL
		  AND po.status IN `+openPOStatuses+`
		  AND po.expected_date IS NOT NULL
		  AND ($2::uuid IS NULL OR po.organization_id = $2)
		ORDER BY po.expected_date ASC
		LIMIT 10
	`, tenantID, orgArg)
	if uErr == nil {
		for uRows.Next() {
			var ud upcomingDelivery
			if uRows.Scan(&ud.ID, &ud.Number, &ud.VendorName, &ud.Status, &ud.TotalAmount, &ud.ExpectedDate, &ud.Overdue) == nil {
				upcoming = append(upcoming, ud)
			}
		}
		uRows.Close()
	}

	response.Success(c, gin.H{
		"totals": gin.H{
			"orders_count":  ordersCount,
			"orders_sum":    ordersSum,
			"pending_count": pendingCount,
			"pending_sum":   pendingSum,
			"overdue_count": overdueCount,
			"payable_total": payableTotal,
		},
		"monthly_series":      monthlySeries,
		"top_suppliers":       topSuppliers,
		"recent_orders":       recentOrders,
		"upcoming_deliveries": upcoming,
	})
}

// GetSupplierKPIs — GET /purchase-orders/supplier-kpis. One row per supplier
// with the aggregates the Yetkazib beruvchilar surfaces need: total spend,
// open pipeline, AP balance (GL 6010 by contact — the akt-sverka source),
// average delivery days (order_date → completed GR receipt_date), on-time
// rate against expected_date, returns count and the manual rating. The old
// SupplierPerformance tab recomputed a subset of this client-side over the
// whole PO list; its issues/lead-time columns were structurally empty.
func (h *Handler) GetSupplierKPIs(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	var orgArg interface{}
	if orgID, okOrg := middleware.GetOrganizationID(c); okOrg && orgID != uuid.Nil {
		orgArg = orgID
	}

	type supplierKPI struct {
		VendorID        uuid.UUID  `json:"vendor_id"`
		Name            string     `json:"name"`
		TotalSpend      float64    `json:"total_spend"`
		OrdersCount     int        `json:"orders_count"`
		OpenPOs         int        `json:"open_pos"`
		OpenAmount      float64    `json:"open_amount"`
		APBalance       float64    `json:"ap_balance"`
		AvgDeliveryDays *float64   `json:"avg_delivery_days"`
		OnTimeRate      *float64   `json:"on_time_rate"`
		ReturnsCount    int        `json:"returns_count"`
		Rating          *float64   `json:"rating"`
		LastOrderDate   *time.Time `json:"last_order_date"`
	}

	kpis := []supplierKPI{}
	rows, err := h.db.Query(`
		SELECT ct.id, ct.name,
		       COALESCE(agg.total_spend, 0), COALESCE(agg.orders_count, 0),
		       COALESCE(agg.open_pos, 0), COALESCE(agg.open_amount, 0),
		       COALESCE(ap.balance, 0),
		       gr.avg_days, gr.on_time_rate,
		       COALESCE(ret.cnt, 0), perf.rating, agg.last_order_date
		FROM contacts ct
		LEFT JOIN LATERAL (
			SELECT SUM(po.total_amount) FILTER (WHERE po.status != 'cancelled') AS total_spend,
			       COUNT(*) FILTER (WHERE po.status != 'cancelled') AS orders_count,
			       COUNT(*) FILTER (WHERE po.status IN `+openPOStatuses+`) AS open_pos,
			       SUM(po.total_amount) FILTER (WHERE po.status IN `+openPOStatuses+`) AS open_amount,
			       MAX(po.order_date) FILTER (WHERE po.status != 'cancelled') AS last_order_date
			FROM purchase_orders po
			WHERE po.vendor_id = ct.id AND po.tenant_id = $1 AND po.deleted_at IS NULL
			  AND ($2::uuid IS NULL OR po.organization_id = $2)
		) agg ON true
		LEFT JOIN LATERAL (
			SELECT SUM(jel.credit_amount - jel.debit_amount) AS balance
			FROM journal_entry_lines jel
			JOIN journal_entries je ON je.id = jel.journal_entry_id AND je.status = 'posted'
			JOIN accounts a ON a.id = jel.account_id
			WHERE je.tenant_id = $1 AND jel.contact_id = ct.id AND a.code = '6010'
			  AND ($2::uuid IS NULL OR je.organization_id = $2)
		) ap ON true
		LEFT JOIN LATERAL (
			SELECT AVG(g.receipt_date::date - po.order_date::date)::float AS avg_days,
			       (COUNT(*) FILTER (WHERE po.expected_date IS NULL OR g.receipt_date::date <= po.expected_date::date))::float
			           / NULLIF(COUNT(*), 0) * 100 AS on_time_rate
			FROM goods_receipts g
			JOIN purchase_orders po ON po.id = g.purchase_order_id
			WHERE g.supplier_id = ct.id AND g.tenant_id = $1
			  AND g.status = 'completed' AND g.deleted_at IS NULL
		) gr ON true
		LEFT JOIN LATERAL (
			SELECT COUNT(*) AS cnt FROM purchase_returns pr
			WHERE pr.supplier_id = ct.id AND pr.tenant_id = $1 AND pr.deleted_at IS NULL
			  AND pr.status NOT IN ('cancelled', 'rejected')
		) ret ON true
		LEFT JOIN LATERAL (
			SELECT AVG(sp.overall_rating)::float AS rating FROM supplier_performance sp
			WHERE sp.vendor_id = ct.id AND sp.tenant_id = $1
		) perf ON true
		WHERE ct.tenant_id = $1 AND ct.type IN ('vendor', 'both') AND ct.deleted_at IS NULL
		ORDER BY COALESCE(agg.total_spend, 0) DESC, ct.name ASC
	`, tenantID, orgArg)
	if err != nil {
		h.log.Error("Failed to load supplier KPIs", "error", err)
		response.InternalError(c, "Failed to load supplier KPIs")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var k supplierKPI
		if rows.Scan(&k.VendorID, &k.Name, &k.TotalSpend, &k.OrdersCount, &k.OpenPOs, &k.OpenAmount,
			&k.APBalance, &k.AvgDeliveryDays, &k.OnTimeRate, &k.ReturnsCount, &k.Rating, &k.LastOrderDate) == nil {
			kpis = append(kpis, k)
		}
	}

	response.Success(c, kpis)
}
