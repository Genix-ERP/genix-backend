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

	paginate, page, pageSize, offset := optPagination(c)
	query := `
		SELECT pm.id, pm.tenant_id, pm.project_id, pm.product_id, pm.product_name, pm.uom,
		       pm.approved_quantity, pm.unit_cost, pm.created_date, pm.updated_date,
		       COALESCE(
		           (SELECT SUM(m.quantity)
		            FROM construction_sub_stage_materials m
		            JOIN construction_sub_stages ss ON ss.id = m.sub_stage_id
		            JOIN construction_stages s ON s.id = ss.stage_id
		            WHERE s.project_id = pm.project_id
		              AND m.tenant_id = pm.tenant_id
		              AND m.product_id = pm.product_id),
		       0) as assigned_quantity
		FROM construction_project_materials pm
		WHERE pm.tenant_id = $1 AND pm.project_id = $2
		ORDER BY pm.product_name, pm.id ASC`
	args := []interface{}{tenantID, projectID}
	if paginate {
		query += " LIMIT $3 OFFSET $4"
		args = append(args, pageSize, offset)
	}
	rows, err := h.db.Query(query, args...)
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
		AssignedQuantity float64   `json:"assigned_quantity"`
	}

	var materials []ProjectMaterial
	for rows.Next() {
		var m ProjectMaterial
		if err := rows.Scan(
			&m.ID, &m.TenantID, &m.ProjectID, &m.ProductID,
			&m.ProductName, &m.UOM,
			&m.ApprovedQuantity, &m.UnitCost,
			&m.CreatedDate, &m.UpdatedDate,
			&m.AssignedQuantity,
		); err != nil {
			h.log.Error("Failed to scan project material", "error", err)
			continue
		}
		materials = append(materials, m)
	}

	if materials == nil {
		materials = []ProjectMaterial{}
	}

	if !paginate {
		response.Success(c, materials)
		return
	}
	var total int
	_ = h.db.QueryRow(
		`SELECT COUNT(*) FROM construction_project_materials pm WHERE pm.tenant_id = $1 AND pm.project_id = $2`,
		tenantID, projectID,
	).Scan(&total)
	response.Paginated(c, materials, page, pageSize, total)
}
