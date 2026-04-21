package handler

import (
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GetDashboardTopProducts returns top selling products by revenue
func (h *Handler) GetDashboardTopProducts(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	rows, err := h.db.Query(`
		SELECT p.name, SUM(sol.quantity) as total_qty, SUM(sol.line_total) as total_revenue
		FROM sales_order_lines sol
		JOIN products p ON sol.product_id = p.id
		JOIN sales_orders so ON sol.sales_order_id = so.id
		WHERE so.tenant_id = $1 AND so.deleted_at IS NULL
		GROUP BY p.name
		HAVING SUM(sol.line_total) > 0
		ORDER BY total_revenue DESC
		LIMIT 5
	`, tenantID)
	if err != nil {
		h.log.Error("Failed to fetch top products", "error", err)
		response.InternalError(c, "Failed to fetch top products")
		return
	}
	defer rows.Close()

	type TopProduct struct {
		Name     string  `json:"name"`
		Quantity float64 `json:"quantity"`
		Revenue  float64 `json:"revenue"`
	}

	var products []TopProduct
	for rows.Next() {
		var p TopProduct
		if err := rows.Scan(&p.Name, &p.Quantity, &p.Revenue); err != nil {
			continue
		}
		products = append(products, p)
	}

	if products == nil {
		products = []TopProduct{}
	}

	response.Success(c, products)
}
