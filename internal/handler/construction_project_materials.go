package handler

import (
	"strconv"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListProjectMaterials returns the tracked materials list for a construction project
func (h *Handler) ListProjectMaterials(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	rows, err := h.db.Query(`
		SELECT id, tenant_id, project_id, product_id, product_name, uom,
		       approved_quantity, unit_cost, created_date, updated_date
		FROM construction_project_materials
		WHERE tenant_id = $1 AND project_id = $2
		ORDER BY product_name
	`, tenantID, projectID)
	if err != nil {
		h.log.Error("Failed to list project materials", "error", err)
		response.InternalError(c, "Failed to list project materials")
		return
	}
	defer rows.Close()

	type ProjectMaterial struct {
		ID               int64     `json:"id"`
		TenantID         string    `json:"tenant_id"`
		ProjectID        int64     `json:"project_id"`
		ProductID        string    `json:"product_id"`
		ProductName      string    `json:"product_name"`
		UOM              string    `json:"uom"`
		ApprovedQuantity float64   `json:"approved_quantity"`
		UnitCost         float64   `json:"unit_cost"`
		CreatedDate      time.Time `json:"created_date"`
		UpdatedDate      time.Time `json:"updated_date"`
	}

	var materials []ProjectMaterial
	for rows.Next() {
		var m ProjectMaterial
		if err := rows.Scan(
			&m.ID, &m.TenantID, &m.ProjectID, &m.ProductID,
			&m.ProductName, &m.UOM,
			&m.ApprovedQuantity, &m.UnitCost,
			&m.CreatedDate, &m.UpdatedDate,
		); err != nil {
			h.log.Error("Failed to scan project material", "error", err)
			continue
		}
		materials = append(materials, m)
	}

	if materials == nil {
		materials = []ProjectMaterial{}
	}

	response.Success(c, materials)
}
