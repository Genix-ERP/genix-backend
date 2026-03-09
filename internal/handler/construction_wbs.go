package handler

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// CONSTRUCTION WBS (Work Breakdown Structure) HANDLERS
// =====================================================

// ListWBSItems returns WBS items for a project, optionally filtered by building
func (h *Handler) ListWBSItems(c *gin.Context) {
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

	// Optional filters
	buildingIDStr := c.Query("building_id")
	parentIDStr := c.Query("parent_id")
	activeOnly := c.DefaultQuery("active_only", "true")

	query := `
		SELECT w.id, w.tenant_id, w.project_id, w.building_id, w.parent_id,
		       w.code, w.name, w.description,
		       w.date_start_plan, w.date_end_plan, w.date_start_actual, w.date_end_actual,
		       w.budget_amount, w.progress, w.sort_order, w.responsible_id,
		       w.is_active, w.created_date, w.updated_date,
		       COALESCE(e.first_name || ' ' || e.last_name, '') as responsible_name,
		       COALESCE((SELECT COUNT(*) FROM construction_wbs c2 WHERE c2.parent_id = w.id AND c2.is_active = true), 0) as children_count,
		       COALESCE(b.name, '') as building_name
		FROM construction_wbs w
		LEFT JOIN employees e ON e.id = w.responsible_id
		LEFT JOIN construction_buildings b ON b.id = w.building_id
		WHERE w.project_id = $1 AND w.tenant_id = $2
	`

	args := []interface{}{projectID, tenantID}
	argCount := 2

	if activeOnly == "true" {
		query += " AND w.is_active = true"
	}

	if buildingIDStr != "" {
		argCount++
		buildingID, err := strconv.ParseInt(buildingIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid building_id")
			return
		}
		query += fmt.Sprintf(" AND w.building_id = $%d", argCount)
		args = append(args, buildingID)
	}

	if parentIDStr != "" {
		argCount++
		if parentIDStr == "null" || parentIDStr == "0" {
			query += " AND w.parent_id IS NULL"
		} else {
			parentID, err := strconv.ParseInt(parentIDStr, 10, 64)
			if err != nil {
				response.BadRequest(c, "Invalid parent_id")
				return
			}
			query += fmt.Sprintf(" AND w.parent_id = $%d", argCount)
			args = append(args, parentID)
		}
	}

	query += " ORDER BY w.sort_order ASC, w.code ASC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list WBS items", "error", err)
		response.InternalError(c, "Failed to list WBS items")
		return
	}
	defer rows.Close()

	items := []entity.ConstructionWBS{}
	for rows.Next() {
		var item entity.ConstructionWBS
		if err := rows.Scan(
			&item.ID, &item.TenantID, &item.ProjectID, &item.BuildingID, &item.ParentID,
			&item.Code, &item.Name, &item.Description,
			&item.DateStartPlan, &item.DateEndPlan, &item.DateStartActual, &item.DateEndActual,
			&item.BudgetAmount, &item.Progress, &item.SortOrder, &item.ResponsibleID,
			&item.IsActive, &item.CreatedDate, &item.UpdatedDate,
			&item.ResponsibleName, &item.ChildrenCount, &item.BuildingName,
		); err != nil {
			h.log.Error("Failed to scan WBS item", "error", err)
			continue
		}
		items = append(items, item)
	}

	response.Success(c, items)
}

// GetWBSTree returns WBS items as a nested tree structure
func (h *Handler) GetWBSTree(c *gin.Context) {
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

	buildingIDStr := c.Query("building_id")

	query := `
		SELECT w.id, w.tenant_id, w.project_id, w.building_id, w.parent_id,
		       w.code, w.name, w.description,
		       w.date_start_plan, w.date_end_plan, w.date_start_actual, w.date_end_actual,
		       w.budget_amount, w.progress, w.sort_order, w.responsible_id,
		       w.is_active, w.created_date, w.updated_date,
		       COALESCE(e.first_name || ' ' || e.last_name, '') as responsible_name,
		       COALESCE((SELECT COUNT(*) FROM construction_wbs c2 WHERE c2.parent_id = w.id AND c2.is_active = true), 0) as children_count,
		       COALESCE(b.name, '') as building_name
		FROM construction_wbs w
		LEFT JOIN employees e ON e.id = w.responsible_id
		LEFT JOIN construction_buildings b ON b.id = w.building_id
		WHERE w.project_id = $1 AND w.tenant_id = $2 AND w.is_active = true
	`

	args := []interface{}{projectID, tenantID}
	argCount := 2

	if buildingIDStr != "" {
		argCount++
		buildingID, err := strconv.ParseInt(buildingIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid building_id")
			return
		}
		query += fmt.Sprintf(" AND w.building_id = $%d", argCount)
		args = append(args, buildingID)
	}

	query += " ORDER BY w.sort_order ASC, w.code ASC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to get WBS tree", "error", err)
		response.InternalError(c, "Failed to get WBS tree")
		return
	}
	defer rows.Close()

	items := []entity.ConstructionWBS{}
	for rows.Next() {
		var item entity.ConstructionWBS
		if err := rows.Scan(
			&item.ID, &item.TenantID, &item.ProjectID, &item.BuildingID, &item.ParentID,
			&item.Code, &item.Name, &item.Description,
			&item.DateStartPlan, &item.DateEndPlan, &item.DateStartActual, &item.DateEndActual,
			&item.BudgetAmount, &item.Progress, &item.SortOrder, &item.ResponsibleID,
			&item.IsActive, &item.CreatedDate, &item.UpdatedDate,
			&item.ResponsibleName, &item.ChildrenCount, &item.BuildingName,
		); err != nil {
			h.log.Error("Failed to scan WBS item", "error", err)
			continue
		}
		items = append(items, item)
	}

	// Build tree: find roots and recursively attach children
	roots := []interface{}{}
	for _, item := range items {
		if !item.ParentID.Valid || item.ParentID.Int64 == 0 {
			roots = append(roots, buildWBSTreeNode(item, items))
		}
	}

	response.Success(c, roots)
}

// wbsTreeNode is a WBS item with nested children for tree rendering
type wbsTreeNode struct {
	entity.ConstructionWBS
	Children []interface{} `json:"children"`
}

// buildWBSTreeNode recursively builds a tree node with its children
func buildWBSTreeNode(item entity.ConstructionWBS, allItems []entity.ConstructionWBS) wbsTreeNode {
	node := wbsTreeNode{
		ConstructionWBS: item,
		Children:        []interface{}{},
	}
	for _, child := range allItems {
		if child.ParentID.Valid && child.ParentID.Int64 == item.ID {
			node.Children = append(node.Children, buildWBSTreeNode(child, allItems))
		}
	}
	return node
}

// CreateWBSItem creates a new WBS item
func (h *Handler) CreateWBSItem(c *gin.Context) {
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

	var req entity.CreateWBSInput
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Auto-generate code if not provided
	if req.Code == "" {
		var maxCode sql.NullString
		_ = h.db.QueryRow(
			`SELECT MAX(code) FROM construction_wbs WHERE project_id = $1 AND tenant_id = $2 AND parent_id IS NOT DISTINCT FROM $3`,
			projectID, tenantID, nullInt64FromVal(req.ParentID),
		).Scan(&maxCode)

		if req.ParentID > 0 {
			// Get parent code
			var parentCode string
			_ = h.db.QueryRow(
				`SELECT code FROM construction_wbs WHERE id = $1 AND tenant_id = $2`,
				req.ParentID, tenantID,
			).Scan(&parentCode)

			// Count siblings
			var siblingCount int
			_ = h.db.QueryRow(
				`SELECT COUNT(*) FROM construction_wbs WHERE parent_id = $1 AND tenant_id = $2 AND is_active = true`,
				req.ParentID, tenantID,
			).Scan(&siblingCount)

			req.Code = fmt.Sprintf("%s.%d", parentCode, siblingCount+1)
		} else {
			var rootCount int
			_ = h.db.QueryRow(
				`SELECT COUNT(*) FROM construction_wbs WHERE project_id = $1 AND tenant_id = $2 AND parent_id IS NULL AND is_active = true`,
				projectID, tenantID,
			).Scan(&rootCount)
			req.Code = fmt.Sprintf("%d", rootCount+1)
		}
	}

	// Parse optional dates
	var dateStartPlan, dateEndPlan interface{}
	if req.DateStartPlan != "" {
		dateStartPlan = req.DateStartPlan
	}
	if req.DateEndPlan != "" {
		dateEndPlan = req.DateEndPlan
	}

	query := `
		INSERT INTO construction_wbs (
			tenant_id, project_id, building_id, parent_id,
			code, name, description,
			date_start_plan, date_end_plan,
			budget_amount, sort_order, responsible_id,
			is_active, created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, true, NOW(), NOW())
		RETURNING id, code, created_date
	`

	var itemID int64
	var code string
	var createdDate time.Time

	err = h.db.QueryRow(query,
		tenantID,
		projectID,
		nullInt64FromVal(req.BuildingID),
		nullInt64FromVal(req.ParentID),
		req.Code,
		req.Name,
		nullStringFromVal(req.Description),
		dateStartPlan,
		dateEndPlan,
		req.BudgetAmount,
		req.SortOrder,
		nullUUIDFromVal(req.ResponsibleID),
	).Scan(&itemID, &code, &createdDate)

	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			response.Conflict(c, "WBS code already exists in this project")
			return
		}
		h.log.Error("Failed to create WBS item", "error", err)
		response.InternalError(c, "Failed to create WBS item")
		return
	}

	// Log activity
	userID, _ := middleware.GetUserID(c)
	h.logConstructionActivity(tenantID, projectID, userID, "wbs", fmt.Sprintf("WBS yaratildi: %s - %s", code, req.Name), "WBS", itemID)

	response.Success(c, map[string]interface{}{
		"id":           itemID,
		"code":         code,
		"created_date": createdDate,
	})
}

// UpdateWBSItem updates an existing WBS item
func (h *Handler) UpdateWBSItem(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var req entity.UpdateWBSInput
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if req.Name != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, *req.Name)
	}
	if req.Code != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("code = $%d", argCount))
		args = append(args, *req.Code)
	}
	if req.Description != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("description = $%d", argCount))
		args = append(args, *req.Description)
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
	if req.ParentID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("parent_id = $%d", argCount))
		if *req.ParentID == 0 {
			args = append(args, nil)
		} else {
			args = append(args, *req.ParentID)
		}
	}
	if req.DateStartPlan != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("date_start_plan = $%d", argCount))
		if *req.DateStartPlan == "" {
			args = append(args, nil)
		} else {
			args = append(args, *req.DateStartPlan)
		}
	}
	if req.DateEndPlan != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("date_end_plan = $%d", argCount))
		if *req.DateEndPlan == "" {
			args = append(args, nil)
		} else {
			args = append(args, *req.DateEndPlan)
		}
	}
	if req.DateStartActual != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("date_start_actual = $%d", argCount))
		if *req.DateStartActual == "" {
			args = append(args, nil)
		} else {
			args = append(args, *req.DateStartActual)
		}
	}
	if req.DateEndActual != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("date_end_actual = $%d", argCount))
		if *req.DateEndActual == "" {
			args = append(args, nil)
		} else {
			args = append(args, *req.DateEndActual)
		}
	}
	if req.BudgetAmount != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("budget_amount = $%d", argCount))
		args = append(args, *req.BudgetAmount)
	}
	if req.Progress != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("progress = $%d", argCount))
		args = append(args, *req.Progress)
	}
	if req.SortOrder != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("sort_order = $%d", argCount))
		args = append(args, *req.SortOrder)
	}
	if req.ResponsibleID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("responsible_id = $%d", argCount))
		if *req.ResponsibleID == "" {
			args = append(args, nil)
		} else {
			parsed, err := uuid.Parse(*req.ResponsibleID)
			if err != nil {
				response.BadRequest(c, "Invalid responsible_id")
				return
			}
			args = append(args, parsed)
		}
	}
	if req.IsActive != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("is_active = $%d", argCount))
		args = append(args, *req.IsActive)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	// Add updated_date
	argCount++
	updates = append(updates, fmt.Sprintf("updated_date = $%d", argCount))
	args = append(args, time.Now())

	// Add WHERE clause params
	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(
		"UPDATE construction_wbs SET %s WHERE id = $%d AND tenant_id = $%d",
		strings.Join(updates, ", "), argCount-1, argCount,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			response.Conflict(c, "WBS code already exists in this project")
			return
		}
		h.log.Error("Failed to update WBS item", "error", err)
		response.InternalError(c, "Failed to update WBS item")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "WBS item not found")
		return
	}

	response.Success(c, map[string]interface{}{
		"id":      id,
		"message": "WBS item updated successfully",
	})
}

// DeleteWBSItem soft-deletes a WBS item (only if no active children)
func (h *Handler) DeleteWBSItem(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	// Check for active children
	var childCount int
	err = h.db.QueryRow(
		`SELECT COUNT(*) FROM construction_wbs WHERE parent_id = $1 AND is_active = true AND tenant_id = $2`,
		id, tenantID,
	).Scan(&childCount)
	if err != nil {
		h.log.Error("Failed to check WBS children", "error", err)
		response.InternalError(c, "Failed to check children")
		return
	}

	if childCount > 0 {
		response.BadRequest(c, fmt.Sprintf("Cannot delete WBS item with %d active children. Delete children first.", childCount))
		return
	}

	// Soft delete
	result, err := h.db.Exec(
		`UPDATE construction_wbs SET is_active = false, updated_date = NOW() WHERE id = $1 AND tenant_id = $2`,
		id, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to delete WBS item", "error", err)
		response.InternalError(c, "Failed to delete WBS item")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "WBS item not found")
		return
	}

	// Log activity
	userID, _ := middleware.GetUserID(c)
	var projectID int64
	_ = h.db.QueryRow(`SELECT project_id FROM construction_wbs WHERE id = $1`, id).Scan(&projectID)
	h.logConstructionActivity(tenantID, projectID, userID, "wbs", "WBS band o'chirildi", "WBS", id)

	response.Success(c, map[string]interface{}{
		"message": "WBS item deleted successfully",
	})
}

// ReorderWBSItems updates sort_order and parent_id for multiple WBS items
func (h *Handler) ReorderWBSItems(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var req entity.ReorderWBSInput
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to begin transaction", "error", err)
		response.InternalError(c, "Failed to reorder")
		return
	}
	defer tx.Rollback()

	for _, item := range req.Items {
		var parentID interface{}
		if item.ParentID > 0 {
			parentID = item.ParentID
		}
		_, err := tx.Exec(
			`UPDATE construction_wbs SET sort_order = $1, parent_id = $2, updated_date = NOW() WHERE id = $3 AND tenant_id = $4`,
			item.SortOrder, parentID, item.ID, tenantID,
		)
		if err != nil {
			h.log.Error("Failed to reorder WBS item", "error", err, "id", item.ID)
			response.InternalError(c, "Failed to reorder WBS items")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit reorder", "error", err)
		response.InternalError(c, "Failed to reorder WBS items")
		return
	}

	response.Success(c, map[string]interface{}{
		"message": "WBS items reordered successfully",
	})
}

// =====================================================
// HELPER FUNCTIONS
// =====================================================

func nullInt64FromVal(val int64) interface{} {
	if val == 0 {
		return nil
	}
	return val
}

func nullStringFromVal(val string) interface{} {
	if val == "" {
		return nil
	}
	return val
}

func nullUUIDFromVal(val string) interface{} {
	if val == "" {
		return nil
	}
	parsed, err := uuid.Parse(val)
	if err != nil {
		return nil
	}
	return parsed
}
