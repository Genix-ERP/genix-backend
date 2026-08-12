package handler

// GET /products/stats — Ombor → Mahsulotlar sahifasidagi to'rtta summary
// karta (Jami mahsulotlar / Faol / Omborda / Kam qolgan) uchun yagona
// endpoint. Ilgari bu raqamlarni web ham, mobil ham o'zi sanardi va
// natijada bir xil tenantda turli qiymat ko'rsatardi:
//   - web `is_active`ni 5000 tagacha yuklangan context massividan,
//     mobil esa faqat ochilgan 20 qatorlik sahifadan sanardi;
//   - mobil "Omborda" o'rniga `is_stockable` bayrog'ini sanardi — bu
//     mahsulot atributi, real qoldiq emas.
//
// Filtrlar ro'yxat endpointi (ListProducts) bilan bir xil, shuning uchun
// kartalar doim tagidagi jadval bilan bitta to'plamni ko'rsatadi.
//
// Kam qolgan chegarasi uchun yangi formula yozilmagan — inventory_stats.go
// dagi lowStockThresholdExpr qayta ishlatiladi, aks holda bu karta Asosiy
// panel bilan yana kelishmay qoladi.

import (
	"fmt"
	"strings"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GetProductStats godoc
// @Summary Product summary stats
// @Description Counts for the product summary cards, using the same filters as GET /products
// @Tags Products
// @Produce json
// @Param search query string false "Search by code, name, sku, barcode or search key"
// @Param category_id query string false "Filter by category ID"
// @Param type query string false "Filter by product type (product/service)"
// @Param inventory_type query string false "Filter by inventory type"
// @Param warehouse_id query string false "Only products with an inventory row in this warehouse"
// @Param include_inactive query boolean false "Include inactive products"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /products/stats [get]
func (h *Handler) GetProductStats(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var orgArg interface{}
	if orgID, okOrg := middleware.GetOrganizationID(c); okOrg && orgID != uuid.Nil {
		orgArg = orgID
	}

	// $1 = tenant, $2 = organization (NULL bo'lishi mumkin)
	args := []interface{}{tenantID, orgArg}
	next := func(v interface{}) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	where := []string{"p.tenant_id = $1", "p.deleted_at IS NULL"}

	if c.Query("include_inactive") != "true" {
		where = append(where, "p.is_active = true")
	}
	if v := c.Query("category_id"); v != "" {
		where = append(where, "p.category_id = "+next(v))
	}
	if v := c.Query("type"); v != "" {
		where = append(where, "p.type = "+next(v))
	}
	if v := c.Query("inventory_type"); v != "" {
		where = append(where, "COALESCE(p.inventory_type, 'trade') = "+next(v))
	}
	// warehouse_id UUID sifatida tekshiriladi — ListProducts'dagi kabi,
	// aks holda noto'g'ri qiymat 500 bo'lib qaytadi.
	if v := c.Query("warehouse_id"); v != "" {
		if _, parseErr := uuid.Parse(v); parseErr == nil {
			where = append(where, fmt.Sprintf(`EXISTS (
				SELECT 1 FROM inventory inv
				WHERE inv.product_id = p.id
				  AND inv.warehouse_id = %s
				  AND inv.tenant_id = $1
			)`, next(v)))
		}
	}
	if v := c.Query("search"); v != "" {
		ph := next("%" + v + "%")
		where = append(where, fmt.Sprintf(
			"(p.code ILIKE %[1]s OR p.name ILIKE %[1]s OR p.sku ILIKE %[1]s"+
				" OR p.barcode ILIKE %[1]s OR p.search_key ILIKE %[1]s)", ph))
	}

	// Mahsulot faqat o'zi tegishli kompaniyada ko'rinishi uchun —
	// ListProducts'dagi bilan bir xil INNER JOIN.
	orgJoin := ""
	if orgArg != nil {
		orgJoin = "INNER JOIN product_organization_settings pos" +
			" ON pos.product_id = p.id AND pos.organization_id = $2"
	}

	query := `
		WITH filtered AS (
			SELECT p.id,
			       p.is_active,
			       COALESCE(p.track_inventory, true) AS track_inventory,
			       COALESCE(s.on_hand, 0) AS on_hand,
			       ` + lowStockThresholdExpr + ` AS threshold
			FROM products p
			` + orgJoin + `
			LEFT JOIN LATERAL (
				SELECT SUM(i.quantity_on_hand) AS on_hand
				FROM inventory i
				JOIN warehouses w ON w.id = i.warehouse_id AND w.deleted_at IS NULL
					AND COALESCE(w.warehouse_type, 'regular') <> 'scrap'
				WHERE i.product_id = p.id
				  AND i.tenant_id = p.tenant_id
				  AND ($2::uuid IS NULL OR w.organization_id = $2)
			) s ON TRUE
			WHERE ` + strings.Join(where, " AND ") + `
		)
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE is_active),
		       COUNT(*) FILTER (WHERE NOT is_active),
		       COUNT(*) FILTER (WHERE on_hand > 0),
		       COUNT(*) FILTER (WHERE is_active AND track_inventory
		                          AND threshold > 0 AND on_hand <= threshold)
		FROM filtered
	`

	var total, active, inactive, inStock, lowStock int
	if err := h.db.QueryRow(query, args...).
		Scan(&total, &active, &inactive, &inStock, &lowStock); err != nil {
		h.log.Error("Failed to compute product stats", "error", err)
		response.InternalError(c, "Failed to compute product stats")
		return
	}

	response.Success(c, gin.H{
		"total":     total,
		"active":    active,
		"inactive":  inactive,
		"in_stock":  inStock,
		"low_stock": lowStock,
	})
}
