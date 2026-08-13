package handler

import (
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GetDashboardTopProducts returns top selling products by revenue for the
// Asosiy panel. Period defaults to the current month ([from, to] on
// order_date, same convention as the /stats family); org-scoped; only real
// orders count (quotations/cancelled excluded via realSalesOrderFilter).
func (h *Handler) GetDashboardTopProducts(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	var orgArg interface{}
	if orgID, okOrg := middleware.GetOrganizationID(c); okOrg && orgID != uuid.Nil {
		orgArg = orgID
	}

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
			to = t.Add(24*time.Hour - time.Microsecond) // inclusive end of day
		}
	}

	rows, err := h.db.Query(`
		SELECT p.id, p.name, SUM(sol.quantity) as total_qty, SUM(sol.line_total) as total_revenue
		FROM sales_order_lines sol
		JOIN products p ON sol.product_id = p.id AND p.deleted_at IS NULL
		JOIN sales_orders so ON sol.sales_order_id = so.id
		WHERE so.tenant_id = $1 AND so.deleted_at IS NULL AND so.`+realSalesOrderFilter+`
		  AND so.order_date >= $2 AND so.order_date <= $3
		  AND ($4::uuid IS NULL OR so.organization_id = $4)
		GROUP BY p.id, p.name
		HAVING SUM(sol.line_total) > 0
		ORDER BY total_revenue DESC
		LIMIT 5
	`, tenantID, from, to, orgArg)
	if err != nil {
		h.log.Error("Failed to fetch top products", "error", err)
		response.InternalError(c, "Failed to fetch top products")
		return
	}
	defer rows.Close()

	type TopProduct struct {
		ProductID uuid.UUID `json:"product_id"`
		Name      string    `json:"name"`
		Quantity  float64   `json:"quantity"`
		Revenue   float64   `json:"revenue"`
	}

	var products []TopProduct
	for rows.Next() {
		var p TopProduct
		if err := rows.Scan(&p.ProductID, &p.Name, &p.Quantity, &p.Revenue); err != nil {
			continue
		}
		products = append(products, p)
	}

	if products == nil {
		products = []TopProduct{}
	}

	response.Success(c, gin.H{
		"period_from": from.Format("2006-01-02"),
		"period_to":   to.Format("2006-01-02"),
		"products":    products,
	})
}
