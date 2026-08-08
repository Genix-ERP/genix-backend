package handler

// GET /sales-orders/stats — one call feeding the whole Savdo "Asosiy panel":
// the stat row, the sales-vs-collections chart, the top-customers chart, the
// recent-orders feed and the overdue-invoices chase list. Mirrors
// GET /purchase-orders/stats (Xarid), GET /inventory/stats (Ombor).
//
// Definitions (savdo-audit.md §8/B):
//   - "Buyurtmalar" counts orders by order_date within [from, to], excluding
//     cancelled and quotation-status rows (a Taklif is not yet an order).
//   - "Daromad (to'langan)" is money actually received in the period: confirmed
//     payment_allocations against sales invoices, by payment_date — not
//     order totals, so the sell-vs-collect gap is visible.
//   - "To'lanmagan" is the OUTSTANDING receivable right now (amount_due over
//     sent/partial/overdue invoices), not a period figure; unpaid_over_30d is
//     the slice of it more than 30 days past due (the red-flag condition).
//   - "Yetkazilmagan" counts open orders (confirmed/processing) — confirmed but
//     not fully shipped.

import (
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// realSalesOrderFilter excludes rows that are not (yet) real orders.
const realSalesOrderFilter = `status NOT IN ('cancelled', 'quotation')`

func (h *Handler) GetSalesOrderStats(c *gin.Context) {
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
			to = t.Add(24*time.Hour - time.Nanosecond) // inclusive end of day
		}
	}

	// ── Totals ──────────────────────────────────────────────────────────
	var ordersCount int
	var ordersSum float64
	h.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(total_amount), 0)
		FROM sales_orders
		WHERE tenant_id = $1 AND deleted_at IS NULL AND `+realSalesOrderFilter+`
		  AND order_date >= $2 AND order_date <= $3
		  AND ($4::uuid IS NULL OR organization_id = $4)
	`, tenantID, from, to, orgArg).Scan(&ordersCount, &ordersSum)

	var revenuePaid float64
	h.db.QueryRow(`
		SELECT COALESCE(SUM(pa.amount), 0)
		FROM payment_allocations pa
		JOIN payments p ON p.id = pa.payment_id
		WHERE p.tenant_id = $1 AND p.status = 'confirmed' AND p.type = 'receipt'
		  AND pa.document_type = 'sales_invoice'
		  AND p.payment_date >= $2 AND p.payment_date <= $3
		  AND ($4::uuid IS NULL OR p.organization_id = $4)
	`, tenantID, from, to, orgArg).Scan(&revenuePaid)

	var unpaidTotal, unpaidOver30 float64
	var overdueInvoiceCount int
	h.db.QueryRow(`
		SELECT COALESCE(SUM(amount_due), 0),
		       COALESCE(SUM(amount_due) FILTER (WHERE due_date < CURRENT_DATE - INTERVAL '30 days'), 0),
		       COUNT(*) FILTER (WHERE due_date < CURRENT_DATE)
		FROM sales_invoices
		WHERE tenant_id = $1 AND deleted_at IS NULL
		  AND status IN ('sent', 'partial') AND amount_due > 0
		  AND ($2::uuid IS NULL OR organization_id = $2)
	`, tenantID, orgArg).Scan(&unpaidTotal, &unpaidOver30, &overdueInvoiceCount)

	var undeliveredCount int
	h.db.QueryRow(`
		SELECT COUNT(*)
		FROM sales_orders
		WHERE tenant_id = $1 AND deleted_at IS NULL AND status IN ('confirmed', 'processing')
		  AND ($2::uuid IS NULL OR organization_id = $2)
	`, tenantID, orgArg).Scan(&undeliveredCount)

	// ── Chart 1: last 6 months, order totals vs collected payments ──────
	type monthPoint struct {
		Month     string  `json:"month"`
		OrdersSum float64 `json:"orders_sum"`
		PaidSum   float64 `json:"paid_sum"`
	}
	series := make([]monthPoint, 0, 6)
	seriesStart := time.Date(nowT.Year(), nowT.Month(), 1, 0, 0, 0, 0, nowT.Location()).AddDate(0, -5, 0)
	byMonth := map[string]*monthPoint{}
	for i := 0; i < 6; i++ {
		m := seriesStart.AddDate(0, i, 0)
		key := m.Format("2006-01")
		series = append(series, monthPoint{Month: key})
		byMonth[key] = &series[len(series)-1]
	}
	oRows, oErr := h.db.Query(`
		SELECT TO_CHAR(DATE_TRUNC('month', order_date), 'YYYY-MM'), COALESCE(SUM(total_amount), 0)
		FROM sales_orders
		WHERE tenant_id = $1 AND deleted_at IS NULL AND `+realSalesOrderFilter+`
		  AND order_date >= $2 AND ($3::uuid IS NULL OR organization_id = $3)
		GROUP BY 1
	`, tenantID, seriesStart, orgArg)
	if oErr == nil {
		for oRows.Next() {
			var key string
			var sum float64
			if oRows.Scan(&key, &sum) == nil {
				if p, okM := byMonth[key]; okM {
					p.OrdersSum = sum
				}
			}
		}
		oRows.Close()
	}
	pRows, pErr := h.db.Query(`
		SELECT TO_CHAR(DATE_TRUNC('month', p.payment_date), 'YYYY-MM'), COALESCE(SUM(pa.amount), 0)
		FROM payment_allocations pa
		JOIN payments p ON p.id = pa.payment_id
		WHERE p.tenant_id = $1 AND p.status = 'confirmed' AND p.type = 'receipt'
		  AND pa.document_type = 'sales_invoice'
		  AND p.payment_date >= $2 AND ($3::uuid IS NULL OR p.organization_id = $3)
		GROUP BY 1
	`, tenantID, seriesStart, orgArg)
	if pErr == nil {
		for pRows.Next() {
			var key string
			var sum float64
			if pRows.Scan(&key, &sum) == nil {
				if p, okM := byMonth[key]; okM {
					p.PaidSum = sum
				}
			}
		}
		pRows.Close()
	}

	// ── Chart 2: top customers by period revenue (top 6 + Boshqa) ───────
	type topCustomer struct {
		CustomerID string  `json:"customer_id"`
		Name       string  `json:"name"`
		Total      float64 `json:"total"`
	}
	topCustomers := []topCustomer{}
	var othersTotal float64
	tcRows, tcErr := h.db.Query(`
		WITH ranked AS (
			SELECT so.customer_id, COALESCE(c.name, 'Noma''lum') AS name,
			       SUM(so.total_amount) AS total,
			       ROW_NUMBER() OVER (ORDER BY SUM(so.total_amount) DESC) AS rn
			FROM sales_orders so
			LEFT JOIN contacts c ON c.id = so.customer_id
			WHERE so.tenant_id = $1 AND so.deleted_at IS NULL AND so.`+realSalesOrderFilter+`
			  AND so.order_date >= $2 AND so.order_date <= $3
			  AND ($4::uuid IS NULL OR so.organization_id = $4)
			GROUP BY so.customer_id, c.name
		)
		SELECT customer_id, name, total, rn FROM ranked ORDER BY rn
	`, tenantID, from, to, orgArg)
	if tcErr == nil {
		for tcRows.Next() {
			var tc topCustomer
			var rn int
			if tcRows.Scan(&tc.CustomerID, &tc.Name, &tc.Total, &rn) == nil {
				if rn <= 6 {
					topCustomers = append(topCustomers, tc)
				} else {
					othersTotal += tc.Total
				}
			}
		}
		tcRows.Close()
	}
	if othersTotal > 0 {
		topCustomers = append(topCustomers, topCustomer{Name: "Boshqa", Total: othersTotal})
	}

	// ── Recent orders (last 10, any period) ─────────────────────────────
	recentOrders := []map[string]interface{}{}
	roRows, roErr := h.db.Query(`
		SELECT so.id, so.order_number, COALESCE(c.name, ''), so.status, so.payment_status,
		       COALESCE(so.total_amount, 0), so.order_date
		FROM sales_orders so
		LEFT JOIN contacts c ON c.id = so.customer_id
		WHERE so.tenant_id = $1 AND so.deleted_at IS NULL AND so.status != 'cancelled'
		  AND ($2::uuid IS NULL OR so.organization_id = $2)
		ORDER BY so.created_at DESC LIMIT 10
	`, tenantID, orgArg)
	if roErr == nil {
		for roRows.Next() {
			var id, number, custName, status, payStatus string
			var total float64
			var orderDate time.Time
			if roRows.Scan(&id, &number, &custName, &status, &payStatus, &total, &orderDate) == nil {
				recentOrders = append(recentOrders, map[string]interface{}{
					"id": id, "order_number": number, "customer_name": custName,
					"status": status, "payment_status": payStatus,
					"total_amount": total, "order_date": orderDate.Format("2006-01-02"),
				})
			}
		}
		roRows.Close()
	}

	// ── Overdue invoices chase list (worst 10 by days overdue) ──────────
	overdueInvoices := []map[string]interface{}{}
	oiRows, oiErr := h.db.Query(`
		SELECT si.id, si.invoice_number, COALESCE(NULLIF(si.customer_name, ''), c.name, ''),
		       si.amount_due, si.due_date, (CURRENT_DATE - si.due_date) AS days_overdue
		FROM sales_invoices si
		LEFT JOIN contacts c ON c.id = si.customer_id
		WHERE si.tenant_id = $1 AND si.deleted_at IS NULL
		  AND si.status IN ('sent', 'partial') AND si.amount_due > 0
		  AND si.due_date < CURRENT_DATE
		  AND ($2::uuid IS NULL OR si.organization_id = $2)
		ORDER BY si.due_date ASC LIMIT 10
	`, tenantID, orgArg)
	if oiErr == nil {
		for oiRows.Next() {
			var id, number, custName string
			var amountDue float64
			var dueDate time.Time
			var daysOverdue int
			if oiRows.Scan(&id, &number, &custName, &amountDue, &dueDate, &daysOverdue) == nil {
				overdueInvoices = append(overdueInvoices, map[string]interface{}{
					"id": id, "invoice_number": number, "customer_name": custName,
					"amount_due": amountDue, "due_date": dueDate.Format("2006-01-02"),
					"days_overdue": daysOverdue,
				})
			}
		}
		oiRows.Close()
	}

	response.Success(c, gin.H{
		"period": gin.H{"from": from.Format("2006-01-02"), "to": to.Format("2006-01-02")},
		"totals": gin.H{
			"orders_count":       ordersCount,
			"orders_sum":         ordersSum,
			"revenue_paid":       revenuePaid,
			"unpaid_total":       unpaidTotal,
			"unpaid_over_30d":    unpaidOver30,
			"overdue_invoices":   overdueInvoiceCount,
			"undelivered_orders": undeliveredCount,
		},
		"monthly_series":   series,
		"top_customers":    topCustomers,
		"recent_orders":    recentOrders,
		"overdue_invoices": overdueInvoices,
	})
}
