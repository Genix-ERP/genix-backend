package handler

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// CONSTRUCTION MATERIAL USAGE HANDLERS
// =====================================================

// ListMaterialUsage returns material usage records for a project
func (h *Handler) ListMaterialUsage(c *gin.Context) {
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

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset := (page - 1) * limit

	// Optional filters
	wbsIDStr := c.Query("wbs_id")
	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")

	countQuery := `SELECT COUNT(*) FROM construction_material_usage WHERE project_id = $1 AND tenant_id = $2`
	countArgs := []interface{}{projectID, tenantID}
	countArgN := 2

	if wbsIDStr != "" {
		countArgN++
		countQuery += fmt.Sprintf(" AND wbs_id = $%d", countArgN)
		wbsID, _ := strconv.ParseInt(wbsIDStr, 10, 64)
		countArgs = append(countArgs, wbsID)
	}
	if dateFrom != "" {
		countArgN++
		countQuery += fmt.Sprintf(" AND usage_date >= $%d", countArgN)
		countArgs = append(countArgs, dateFrom)
	}
	if dateTo != "" {
		countArgN++
		countQuery += fmt.Sprintf(" AND usage_date <= $%d", countArgN)
		countArgs = append(countArgs, dateTo)
	}

	var total int
	if err := h.db.QueryRow(countQuery, countArgs...).Scan(&total); err != nil {
		h.log.Error("Failed to count material usage", "error", err)
		response.InternalError(c, "Failed to count material usage")
		return
	}

	query := `
		SELECT mu.id, mu.tenant_id, mu.project_id, mu.building_id, mu.wbs_id,
		       mu.product_id, mu.product_name, mu.uom, mu.quantity_used,
		       mu.usage_date, mu.estimate_line_id, mu.recorded_by, mu.notes,
		       mu.created_date, mu.updated_date,
		       COALESCE(w.code, '') as wbs_code,
		       COALESCE(w.name, '') as wbs_name,
		       COALESCE(b.name, '') as building_name,
		       COALESCE(u.first_name || ' ' || u.last_name, '') as recorded_name
		FROM construction_material_usage mu
		LEFT JOIN construction_wbs w ON w.id = mu.wbs_id
		LEFT JOIN construction_buildings b ON b.id = mu.building_id
		LEFT JOIN users u ON u.id = mu.recorded_by
		WHERE mu.project_id = $1 AND mu.tenant_id = $2
	`

	args := []interface{}{projectID, tenantID}
	argCount := 2

	if wbsIDStr != "" {
		argCount++
		query += fmt.Sprintf(" AND mu.wbs_id = $%d", argCount)
		wbsID, _ := strconv.ParseInt(wbsIDStr, 10, 64)
		args = append(args, wbsID)
	}
	if dateFrom != "" {
		argCount++
		query += fmt.Sprintf(" AND mu.usage_date >= $%d", argCount)
		args = append(args, dateFrom)
	}
	if dateTo != "" {
		argCount++
		query += fmt.Sprintf(" AND mu.usage_date <= $%d", argCount)
		args = append(args, dateTo)
	}

	query += fmt.Sprintf(" ORDER BY mu.usage_date DESC, mu.id DESC LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list material usage", "error", err)
		response.InternalError(c, "Failed to list material usage")
		return
	}
	defer rows.Close()

	items := []map[string]interface{}{}
	for rows.Next() {
		var id, projectIDVal int64
		var buildingID, wbsID, estimateLineID sql.NullInt64
		var productID uuid.NullUUID
		var productName, uom string
		var quantityUsed float64
		var usageDate time.Time
		var recordedBy uuid.NullUUID
		var notes sql.NullString
		var createdDate, updatedDate time.Time
		var wbsCode, wbsName, buildingName, recordedName string

		if err := rows.Scan(
			&id, &tenantID, &projectIDVal, &buildingID, &wbsID,
			&productID, &productName, &uom, &quantityUsed,
			&usageDate, &estimateLineID, &recordedBy, &notes,
			&createdDate, &updatedDate,
			&wbsCode, &wbsName, &buildingName, &recordedName,
		); err != nil {
			h.log.Error("Failed to scan material usage", "error", err)
			continue
		}

		item := map[string]interface{}{
			"id":               id,
			"project_id":       projectIDVal,
			"building_id":      nullInt64Val(buildingID),
			"wbs_id":           nullInt64Val(wbsID),
			"product_id":       nullUUIDVal(productID),
			"product_name":     productName,
			"uom":              uom,
			"quantity_used":    quantityUsed,
			"usage_date":       usageDate.Format("2006-01-02"),
			"estimate_line_id": nullInt64Val(estimateLineID),
			"recorded_by":      nullUUIDVal(recordedBy),
			"notes":            nullStringVal(notes),
			"created_date":     createdDate,
			"updated_date":     updatedDate,
			"wbs_code":         wbsCode,
			"wbs_name":         wbsName,
			"building_name":    buildingName,
			"recorded_name":    recordedName,
		}
		items = append(items, item)
	}

	response.Paginated(c, items, page, limit, total)
}

// CreateMaterialUsage records material consumption on site
func (h *Handler) CreateMaterialUsage(c *gin.Context) {
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

	var req struct {
		ProductID      string  `json:"product_id"`
		ProductName    string  `json:"product_name" binding:"required"`
		UOM            string  `json:"uom" binding:"required"`
		QuantityUsed   float64 `json:"quantity_used" binding:"required"`
		UsageDate      string  `json:"usage_date" binding:"required"`
		WBSID          int64   `json:"wbs_id"`
		BuildingID     int64   `json:"building_id"`
		EstimateLineID int64   `json:"estimate_line_id"`
		Notes          string  `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid input: product_name, uom, quantity_used, and usage_date are required")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var productID interface{}
	if req.ProductID != "" {
		if parsed, err := uuid.Parse(req.ProductID); err == nil {
			productID = parsed
		}
	}

	var id int64
	err = h.db.QueryRow(`
		INSERT INTO construction_material_usage (
			tenant_id, project_id, building_id, wbs_id,
			product_id, product_name, uom, quantity_used,
			usage_date, estimate_line_id, recorded_by, notes,
			created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW(), NOW())
		RETURNING id
	`, tenantID, projectID,
		nullInt64FromVal(req.BuildingID), nullInt64FromVal(req.WBSID),
		productID, req.ProductName, req.UOM, req.QuantityUsed,
		req.UsageDate, nullInt64FromVal(req.EstimateLineID), userID, nullStringFromVal(req.Notes),
	).Scan(&id)

	if err != nil {
		h.log.Error("Failed to create material usage", "error", err)
		response.InternalError(c, "Failed to record material usage")
		return
	}

	h.logConstructionActivity(tenantID, projectID, userID, "material",
		fmt.Sprintf("Material sarflash: %s %.2f %s", req.ProductName, req.QuantityUsed, req.UOM),
		"MaterialUsage", id)

	response.Created(c, map[string]interface{}{
		"id":      id,
		"message": "Material usage recorded successfully",
	})
}

// UpdateMaterialUsage updates a material usage record
func (h *Handler) UpdateMaterialUsage(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	usageID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var projectID int64
	err = h.db.QueryRow(
		`SELECT project_id FROM construction_material_usage WHERE id = $1 AND tenant_id = $2`,
		usageID, tenantID,
	).Scan(&projectID)
	if err != nil {
		response.NotFound(c, "Material usage not found")
		return
	}

	var req struct {
		ProductName    *string  `json:"product_name"`
		UOM            *string  `json:"uom"`
		QuantityUsed   *float64 `json:"quantity_used"`
		UsageDate      *string  `json:"usage_date"`
		WBSID          *int64   `json:"wbs_id"`
		BuildingID     *int64   `json:"building_id"`
		EstimateLineID *int64   `json:"estimate_line_id"`
		Notes          *string  `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if req.ProductName != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("product_name = $%d", argCount))
		args = append(args, *req.ProductName)
	}
	if req.UOM != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("uom = $%d", argCount))
		args = append(args, *req.UOM)
	}
	if req.QuantityUsed != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("quantity_used = $%d", argCount))
		args = append(args, *req.QuantityUsed)
	}
	if req.UsageDate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("usage_date = $%d", argCount))
		args = append(args, *req.UsageDate)
	}
	if req.WBSID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("wbs_id = $%d", argCount))
		if *req.WBSID == 0 {
			args = append(args, nil)
		} else {
			args = append(args, *req.WBSID)
		}
	}
	if req.BuildingID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("building_id = $%d", argCount))
		if *req.BuildingID == 0 {
			args = append(args, nil)
		} else {
			args = append(args, *req.BuildingID)
		}
	}
	if req.EstimateLineID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("estimate_line_id = $%d", argCount))
		if *req.EstimateLineID == 0 {
			args = append(args, nil)
		} else {
			args = append(args, *req.EstimateLineID)
		}
	}
	if req.Notes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, nullStringFromVal(*req.Notes))
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++
	updates = append(updates, fmt.Sprintf("updated_date = $%d", argCount))
	args = append(args, time.Now())

	argCount++
	args = append(args, usageID)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(
		"UPDATE construction_material_usage SET %s WHERE id = $%d AND tenant_id = $%d",
		strings.Join(updates, ", "), argCount-1, argCount,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update material usage", "error", err)
		response.InternalError(c, "Failed to update material usage")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Material usage not found")
		return
	}

	response.Success(c, map[string]interface{}{
		"id":      usageID,
		"message": "Material usage updated successfully",
	})
}

// DeleteMaterialUsage deletes a material usage record
func (h *Handler) DeleteMaterialUsage(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	usageID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var projectID int64
	err = h.db.QueryRow(
		`SELECT project_id FROM construction_material_usage WHERE id = $1 AND tenant_id = $2`,
		usageID, tenantID,
	).Scan(&projectID)
	if err != nil {
		response.NotFound(c, "Material usage not found")
		return
	}

	_, err = h.db.Exec(`DELETE FROM construction_material_usage WHERE id = $1 AND tenant_id = $2`, usageID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete material usage", "error", err)
		response.InternalError(c, "Failed to delete material usage")
		return
	}

	userID, _ := middleware.GetUserID(c)
	h.logConstructionActivity(tenantID, projectID, userID, "material",
		"Material sarflash yozuvi o'chirildi", "MaterialUsage", usageID)

	response.Success(c, map[string]interface{}{
		"message": "Material usage deleted successfully",
	})
}

// GetMaterialSummary returns smeta vs actual material comparison
func (h *Handler) GetMaterialSummary(c *gin.Context) {
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

	query := `
		SELECT
			el.id as estimate_line_id,
			el.name,
			el.uom,
			el.quantity as planned_qty,
			el.material_rate as unit_price,
			el.material_rate * el.quantity as planned_cost,
			COALESCE(mu.actual_qty, 0) as actual_qty,
			COALESCE(w.code, '') as wbs_code,
			COALESCE(w.name, '') as wbs_name
		FROM construction_estimate_line el
		JOIN construction_estimate e ON e.id = el.estimate_id
		LEFT JOIN construction_wbs w ON w.id = el.wbs_id
		LEFT JOIN LATERAL (
			SELECT SUM(quantity_used) as actual_qty
			FROM construction_material_usage
			WHERE estimate_line_id = el.id AND tenant_id = $2
		) mu ON true
		WHERE e.project_id = $1 AND e.tenant_id = $2 AND e.is_current = true
		ORDER BY el.sort_order, el.id
	`

	rows, err := h.db.Query(query, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to get material summary", "error", err)
		response.InternalError(c, "Failed to get material summary")
		return
	}
	defer rows.Close()

	items := []map[string]interface{}{}
	for rows.Next() {
		var estimateLineID int64
		var name, uom, wbsCode, wbsName string
		var plannedQty, unitPrice, plannedCost, actualQty float64

		if err := rows.Scan(
			&estimateLineID, &name, &uom, &plannedQty, &unitPrice, &plannedCost,
			&actualQty, &wbsCode, &wbsName,
		); err != nil {
			h.log.Error("Failed to scan material summary", "error", err)
			continue
		}

		var status string
		if plannedQty > 0 {
			ratio := actualQty / plannedQty
			if ratio > 1.2 {
				status = "overuse"
			} else if ratio > 1.0 {
				status = "warning"
			} else {
				status = "normal"
			}
		} else {
			status = "normal"
		}

		var diffPct float64
		if plannedQty > 0 {
			diffPct = ((actualQty / plannedQty) - 1) * 100
		}

		items = append(items, map[string]interface{}{
			"estimate_line_id": estimateLineID,
			"name":             name,
			"uom":              uom,
			"planned_qty":      plannedQty,
			"actual_qty":       actualQty,
			"remaining_qty":    plannedQty - actualQty,
			"unit_price":       unitPrice,
			"planned_cost":     plannedCost,
			"actual_cost":      actualQty * unitPrice,
			"diff_pct":         diffPct,
			"status":           status,
			"wbs_code":         wbsCode,
			"wbs_name":         wbsName,
		})
	}

	response.Success(c, items)
}

// Helper functions for null value handling in maps
func nullInt64Val(n sql.NullInt64) interface{} {
	if n.Valid {
		return n.Int64
	}
	return nil
}

func nullUUIDVal(n uuid.NullUUID) interface{} {
	if n.Valid {
		return n.UUID
	}
	return nil
}

func nullStringVal(n sql.NullString) interface{} {
	if n.Valid {
		return n.String
	}
	return nil
}
