package handler

import (
	"fmt"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListProductPackagingMaterials returns default packaging materials for a product.
func (h *Handler) ListProductPackagingMaterials(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	productID := c.Param("id")
	if productID == "" {
		response.BadRequest(c, "Product ID is required")
		return
	}

	query := `
		SELECT ppm.id, ppm.product_id, ppm.material_id,
		       m.name AS material_name, m.code AS material_code,
		       ppm.quantity_per_piece,
		       COALESCE(m.cost_price, 0) AS unit_cost
		FROM product_packaging_materials ppm
		JOIN products m ON m.id = ppm.material_id
		WHERE ppm.tenant_id = $1 AND ppm.product_id = $2
		ORDER BY m.name
	`

	rows, err := h.db.Query(query, tenantID, productID)
	if err != nil {
		h.log.Error("Failed to list product packaging materials", "error", err)
		response.InternalError(c, "Failed to list product packaging materials")
		return
	}
	defer rows.Close()

	type materialRow struct {
		ID               uuid.UUID `json:"id"`
		ProductID        uuid.UUID `json:"product_id"`
		MaterialID       uuid.UUID `json:"material_id"`
		MaterialName     string    `json:"material_name"`
		MaterialCode     string    `json:"material_code"`
		QuantityPerPiece float64   `json:"quantity_per_piece"`
		UnitCost         float64   `json:"unit_cost"`
	}

	results := make([]materialRow, 0)
	for rows.Next() {
		var r materialRow
		if err := rows.Scan(&r.ID, &r.ProductID, &r.MaterialID, &r.MaterialName, &r.MaterialCode, &r.QuantityPerPiece, &r.UnitCost); err != nil {
			h.log.Error("Failed to scan packaging material", "error", err)
			continue
		}
		results = append(results, r)
	}

	response.Success(c, results)
}

// SaveProductPackagingMaterials upserts default packaging materials for a product.
func (h *Handler) SaveProductPackagingMaterials(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	productID := c.Param("id")
	if productID == "" {
		response.BadRequest(c, "Product ID is required")
		return
	}

	var input struct {
		Materials []struct {
			MaterialID       string  `json:"material_id"`
			QuantityPerPiece float64 `json:"quantity_per_piece"`
		} `json:"materials"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	if len(input.Materials) == 0 {
		response.BadRequest(c, "At least one material is required")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to begin tx", "error", err)
		response.InternalError(c, "Failed to save packaging materials")
		return
	}
	defer tx.Rollback()

	for _, m := range input.Materials {
		if m.MaterialID == "" || m.QuantityPerPiece <= 0 {
			continue
		}
		query := `
			INSERT INTO product_packaging_materials (tenant_id, product_id, material_id, quantity_per_piece, updated_at)
			VALUES ($1, $2, $3, $4, NOW())
			ON CONFLICT (tenant_id, product_id, material_id)
			DO UPDATE SET quantity_per_piece = EXCLUDED.quantity_per_piece, updated_at = NOW()
		`
		if _, err := tx.Exec(query, tenantID, productID, m.MaterialID, m.QuantityPerPiece); err != nil {
			h.log.Error("Failed to upsert packaging material", "error", err, "material_id", m.MaterialID)
			response.InternalError(c, fmt.Sprintf("Failed to save material %s", m.MaterialID))
			return
		}
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit tx", "error", err)
		response.InternalError(c, "Failed to save packaging materials")
		return
	}

	response.Success(c, map[string]string{"message": "Packaging materials saved"})
}

// DeleteProductPackagingMaterial removes a single default packaging material.
func (h *Handler) DeleteProductPackagingMaterial(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	productID := c.Param("id")
	materialID := c.Param("materialId")
	if productID == "" || materialID == "" {
		response.BadRequest(c, "Product ID and Material ID are required")
		return
	}

	query := `DELETE FROM product_packaging_materials WHERE tenant_id = $1 AND product_id = $2 AND material_id = $3`
	result, err := h.db.Exec(query, tenantID, productID, materialID)
	if err != nil {
		h.log.Error("Failed to delete packaging material", "error", err)
		response.InternalError(c, "Failed to delete packaging material")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Packaging material not found")
		return
	}

	response.Success(c, map[string]string{"message": "Packaging material deleted"})
}
