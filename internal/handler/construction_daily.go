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
// CONSTRUCTION DAILY LOG HANDLERS (WBS-linked progress)
// =====================================================

// ListDailyLogs returns daily log entries for a project
func (h *Handler) ListConstructionDailyLogs(c *gin.Context) {
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

	// Parse pagination
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
	buildingIDStr := c.Query("building_id")
	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")

	// Count query
	countQuery := `SELECT COUNT(*) FROM construction_daily_log WHERE project_id = $1 AND tenant_id = $2`
	countArgs := []interface{}{projectID, tenantID}
	countArgN := 2

	if wbsIDStr != "" {
		countArgN++
		countQuery += fmt.Sprintf(" AND wbs_id = $%d", countArgN)
		wbsID, _ := strconv.ParseInt(wbsIDStr, 10, 64)
		countArgs = append(countArgs, wbsID)
	}
	if buildingIDStr != "" {
		countArgN++
		countQuery += fmt.Sprintf(" AND building_id = $%d", countArgN)
		buildingID, _ := strconv.ParseInt(buildingIDStr, 10, 64)
		countArgs = append(countArgs, buildingID)
	}
	if dateFrom != "" {
		countArgN++
		countQuery += fmt.Sprintf(" AND date >= $%d", countArgN)
		countArgs = append(countArgs, dateFrom)
	}
	if dateTo != "" {
		countArgN++
		countQuery += fmt.Sprintf(" AND date <= $%d", countArgN)
		countArgs = append(countArgs, dateTo)
	}

	var total int
	if err := h.db.QueryRow(countQuery, countArgs...).Scan(&total); err != nil {
		h.log.Error("Failed to count daily logs", "error", err)
		response.InternalError(c, "Failed to count daily logs")
		return
	}

	// Data query
	query := `
		SELECT d.id, d.tenant_id, d.project_id, d.building_id, d.stage_id,
		       d.date, d.end_date,
		       d.workers_count, d.expected_budget, d.weather, d.description, d.issues,
		       d.reported_by, d.created_date, d.updated_date,
		       COALESCE(b.name, '') as building_name,
		       COALESCE(s.name, '') as stage_name,
		       COALESCE(
		           (SELECT ROUND(COUNT(*) FILTER (WHERE ss.status = 'completed') * 100.0 / NULLIF(COUNT(*), 0), 1)
		            FROM construction_sub_stages ss WHERE ss.stage_id = d.stage_id AND ss.tenant_id = d.tenant_id),
		           CASE WHEN s.status = 'completed' THEN 100 ELSE 0 END
		       ) as stage_progress,
		       COALESCE(u.first_name || ' ' || u.last_name, '') as reported_name
		FROM construction_daily_log d
		LEFT JOIN construction_buildings b ON b.id = d.building_id
		LEFT JOIN construction_stages s ON s.id = d.stage_id
		LEFT JOIN users u ON u.id = d.reported_by
		WHERE d.project_id = $1 AND d.tenant_id = $2
	`

	args := []interface{}{projectID, tenantID}
	argCount := 2

	if wbsIDStr != "" {
		argCount++
		query += fmt.Sprintf(" AND d.wbs_id = $%d", argCount)
		wbsID, _ := strconv.ParseInt(wbsIDStr, 10, 64)
		args = append(args, wbsID)
	}
	if buildingIDStr != "" {
		argCount++
		query += fmt.Sprintf(" AND d.building_id = $%d", argCount)
		buildingID, _ := strconv.ParseInt(buildingIDStr, 10, 64)
		args = append(args, buildingID)
	}
	if dateFrom != "" {
		argCount++
		query += fmt.Sprintf(" AND d.date >= $%d", argCount)
		args = append(args, dateFrom)
	}
	if dateTo != "" {
		argCount++
		query += fmt.Sprintf(" AND d.date <= $%d", argCount)
		args = append(args, dateTo)
	}

	query += fmt.Sprintf(" ORDER BY d.date DESC, d.id DESC LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list daily logs", "error", err)
		response.InternalError(c, "Failed to list daily logs")
		return
	}
	defer rows.Close()

	items := []entity.ConstructionDailyLog{}
	for rows.Next() {
		var item entity.ConstructionDailyLog
		if err := rows.Scan(
			&item.ID, &item.TenantID, &item.ProjectID, &item.BuildingID, &item.StageID,
			&item.Date, &item.EndDate,
			&item.WorkersCount, &item.ExpectedBudget, &item.Weather, &item.Description, &item.Issues,
			&item.ReportedBy, &item.CreatedDate, &item.UpdatedDate,
			&item.BuildingName, &item.StageName, &item.StageProgress, &item.ReportedName,
		); err != nil {
			h.log.Error("Failed to scan daily log", "error", err)
			continue
		}
		items = append(items, item)
	}

	response.Paginated(c, items, page, limit, total)
}

// CreateDailyLog creates a new daily log entry and triggers progress recalculation
func (h *Handler) CreateConstructionDailyLog(c *gin.Context) {
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

	var req entity.CreateDailyLogInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	userID, _ := middleware.GetUserID(c)

	var logID int64
	err = h.db.QueryRow(`
		INSERT INTO construction_daily_log (
			tenant_id, project_id, building_id, stage_id,
			date, end_date, workers_count, expected_budget,
			weather, description, issues,
			reported_by, created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW(), NOW())
		RETURNING id
	`, tenantID, projectID, nullInt64FromVal(req.BuildingID), nullInt64FromVal(req.StageID),
		req.Date, nullStringFromVal(req.EndDate),
		req.WorkersCount, req.ExpectedBudget, nullStringFromVal(req.Weather),
		nullStringFromVal(req.Description), nullStringFromVal(req.Issues),
		userID,
	).Scan(&logID)

	if err != nil {
		h.log.Error("Failed to create daily log", "error", err)
		response.InternalError(c, "Failed to create daily log")
		return
	}

	h.logConstructionActivity(tenantID, projectID, userID, "progress",
		"Qurilish jarayoni yozuvi qo'shildi", "DailyLog", logID)

	response.Success(c, map[string]interface{}{
		"id": logID,
	})
}

// UpdateDailyLog updates a daily log entry
func (h *Handler) UpdateConstructionDailyLog(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	logID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	// Verify log exists and belongs to this tenant
	var exists bool
	err = h.db.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM construction_daily_log WHERE id = $1 AND tenant_id = $2)`,
		logID, tenantID,
	).Scan(&exists)
	if err != nil || !exists {
		response.NotFound(c, "Daily log not found")
		return
	}

	var req entity.UpdateDailyLogInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if req.BuildingID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("building_id = $%d", argCount))
		if *req.BuildingID == 0 {
			args = append(args, nil)
		} else {
			args = append(args, *req.BuildingID)
		}
	}
	if req.StageID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("stage_id = $%d", argCount))
		if *req.StageID == 0 {
			args = append(args, nil)
		} else {
			args = append(args, *req.StageID)
		}
	}
	if req.Date != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("date = $%d", argCount))
		args = append(args, *req.Date)
	}
	if req.EndDate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("end_date = $%d", argCount))
		args = append(args, nullStringFromVal(*req.EndDate))
	}
	if req.WorkersCount != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("workers_count = $%d", argCount))
		args = append(args, *req.WorkersCount)
	}
	if req.ExpectedBudget != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("expected_budget = $%d", argCount))
		args = append(args, *req.ExpectedBudget)
	}
	if req.Weather != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("weather = $%d", argCount))
		args = append(args, nullStringFromVal(*req.Weather))
	}
	if req.Description != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("description = $%d", argCount))
		args = append(args, nullStringFromVal(*req.Description))
	}
	if req.Issues != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("issues = $%d", argCount))
		args = append(args, nullStringFromVal(*req.Issues))
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++
	updates = append(updates, fmt.Sprintf("updated_date = $%d", argCount))
	args = append(args, time.Now())

	argCount++
	args = append(args, logID)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(
		"UPDATE construction_daily_log SET %s WHERE id = $%d AND tenant_id = $%d",
		strings.Join(updates, ", "), argCount-1, argCount,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update daily log", "error", err)
		response.InternalError(c, "Failed to update daily log")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Daily log not found")
		return
	}

	response.Success(c, map[string]interface{}{
		"id":      logID,
		"message": "Daily log updated successfully",
	})
}

// DeleteDailyLog deletes a daily log entry
func (h *Handler) DeleteConstructionDailyLog(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	logID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var projectID int64
	err = h.db.QueryRow(
		`SELECT project_id FROM construction_daily_log WHERE id = $1 AND tenant_id = $2`,
		logID, tenantID,
	).Scan(&projectID)
	if err != nil {
		response.NotFound(c, "Daily log not found")
		return
	}

	_, err = h.db.Exec(`DELETE FROM construction_daily_log WHERE id = $1 AND tenant_id = $2`, logID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete daily log", "error", err)
		response.InternalError(c, "Failed to delete daily log")
		return
	}

	userID, _ := middleware.GetUserID(c)
	h.logConstructionActivity(tenantID, projectID, userID, "progress", "Qurilish jarayoni yozuvi o'chirildi", "DailyLog", logID)

	response.Success(c, map[string]interface{}{
		"message": "Daily log deleted successfully",
	})
}

// =====================================================
// THE CRITICAL FUNCTION: PROGRESS RECALCULATION CHAIN
// daily_log → cumulative_qty → WBS progress → Building progress → Project progress
// =====================================================

func (h *Handler) recalculateCumulativeAndProgress(projectID, wbsID int64, tenantID uuid.UUID) {
	// Step 1: Recalculate cumulative_qty for this WBS
	// SUM all quantity_done for this WBS item, ordered by date
	var totalQty float64
	err := h.db.QueryRow(
		`SELECT COALESCE(SUM(quantity_done), 0) FROM construction_daily_log WHERE wbs_id = $1 AND tenant_id = $2`,
		wbsID, tenantID,
	).Scan(&totalQty)
	if err != nil {
		h.log.Error("Failed to sum daily log quantities", "error", err)
		return
	}

	// Update cumulative_qty on the most recent daily log entry
	_, err = h.db.Exec(
		`UPDATE construction_daily_log SET cumulative_qty = $1 WHERE wbs_id = $2 AND tenant_id = $3 AND id = (
			SELECT id FROM construction_daily_log WHERE wbs_id = $2 AND tenant_id = $3 ORDER BY date DESC, id DESC LIMIT 1
		)`,
		totalQty, wbsID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to update cumulative qty", "error", err)
	}

	// Step 2: Calculate WBS progress from estimate line quantity
	// WBS progress = cumulative_qty / estimate_line.quantity * 100
	var estimateQty sql.NullFloat64
	err = h.db.QueryRow(`
		SELECT el.quantity
		FROM construction_estimate_line el
		JOIN construction_estimate e ON e.id = el.estimate_id
		WHERE el.wbs_id = $1 AND e.project_id = $2 AND e.is_current = true AND e.tenant_id = $3
		LIMIT 1
	`, wbsID, projectID, tenantID).Scan(&estimateQty)

	var wbsProgress float64
	if err == nil && estimateQty.Valid && estimateQty.Float64 > 0 {
		wbsProgress = (totalQty / estimateQty.Float64) * 100
		if wbsProgress > 100 {
			wbsProgress = 100
		}
	}

	// Update WBS progress
	_, err = h.db.Exec(
		`UPDATE construction_wbs SET progress = $1, updated_date = NOW() WHERE id = $2 AND tenant_id = $3`,
		wbsProgress, wbsID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to update WBS progress", "error", err)
	}

	// Step 3: Recalculate building progress (weighted average of WBS progress by budget)
	// Get building_id for this WBS
	var buildingID sql.NullInt64
	_ = h.db.QueryRow(
		`SELECT building_id FROM construction_wbs WHERE id = $1 AND tenant_id = $2`,
		wbsID, tenantID,
	).Scan(&buildingID)

	if buildingID.Valid && buildingID.Int64 > 0 {
		h.recalculateBuildingProgress(buildingID.Int64, tenantID)
	}

	// Step 4: Recalculate project progress (weighted average of building/WBS progress)
	h.recalculateProjectProgress(projectID, tenantID)
}

// recalculateBuildingProgress calculates building progress as weighted average of its WBS items
func (h *Handler) recalculateBuildingProgress(buildingID int64, tenantID uuid.UUID) {
	var totalBudget, weightedProgress float64
	rows, err := h.db.Query(
		`SELECT budget_amount, progress FROM construction_wbs WHERE building_id = $1 AND tenant_id = $2 AND is_active = true AND parent_id IS NULL`,
		buildingID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to query WBS for building progress", "error", err)
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var budget, progress float64
		if err := rows.Scan(&budget, &progress); err != nil {
			continue
		}
		if budget > 0 {
			totalBudget += budget
			weightedProgress += budget * progress
		} else {
			// If no budget, use equal weight
			count++
			weightedProgress += progress
		}
	}

	var buildingProgress float64
	if totalBudget > 0 {
		buildingProgress = weightedProgress / totalBudget
	} else if count > 0 {
		buildingProgress = weightedProgress / float64(count)
	}

	_, err = h.db.Exec(
		`UPDATE construction_buildings SET progress_percent = $1, updated_date = NOW() WHERE id = $2 AND tenant_id = $3`,
		buildingProgress, buildingID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to update building progress", "error", err)
	}
}

// recalculateProjectProgress calculates project progress from all root-level WBS items
func (h *Handler) recalculateProjectProgress(projectID int64, tenantID uuid.UUID) {
	var totalBudget, weightedProgress float64
	rows, err := h.db.Query(
		`SELECT budget_amount, progress FROM construction_wbs WHERE project_id = $1 AND tenant_id = $2 AND is_active = true AND parent_id IS NULL`,
		projectID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to query WBS for project progress", "error", err)
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var budget, progress float64
		if err := rows.Scan(&budget, &progress); err != nil {
			continue
		}
		if budget > 0 {
			totalBudget += budget
			weightedProgress += budget * progress
		} else {
			count++
			weightedProgress += progress
		}
	}

	var projectProgress float64
	if totalBudget > 0 {
		projectProgress = weightedProgress / totalBudget
	} else if count > 0 {
		projectProgress = weightedProgress / float64(count)
	}

	_, err = h.db.Exec(
		`UPDATE construction_projects SET progress_percent = $1, updated_date = NOW() WHERE id = $2 AND tenant_id = $3`,
		projectProgress, projectID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to update project progress", "error", err)
	}
}
