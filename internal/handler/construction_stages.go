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
// CONSTRUCTION STAGES HANDLERS
// =====================================================

// ListConstructionStages returns all stages for a project
func (h *Handler) ListConstructionStages(c *gin.Context) {
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

	// Optional building filter (migration 333). Accepts:
	//   ?building_id=<id>    - stages assigned to that building
	//   ?building_id=0       - project-wide stages (no building)
	// Omit the param to get every stage (used by the "Hammasi" tab).
	var buildingFilter sql.NullInt64
	if raw := c.Query("building_id"); raw != "" {
		if v, parseErr := strconv.ParseInt(raw, 10, 64); parseErr == nil {
			buildingFilter = sql.NullInt64{Int64: v, Valid: true}
		}
	}

	args := []interface{}{projectID, tenantID}
	buildingClause := ""
	if buildingFilter.Valid {
		if buildingFilter.Int64 == 0 {
			buildingClause = " AND s.building_id IS NULL"
		} else {
			args = append(args, buildingFilter.Int64)
			buildingClause = " AND s.building_id = $3"
		}
	}

	query := `
		SELECT s.id, s.tenant_id, s.project_id, s.building_id, s.name, s.stage_order,
		       s.status, s.planned_budget, s.planned_start, s.planned_end,
		       s.actual_start, s.actual_end, s.notes, s.created_at, s.updated_at,
		       b.name AS building_name,
		       COALESCE(SUM(CASE WHEN el.status = 'approved' AND el.deleted_at IS NULL THEN el.amount ELSE 0 END), 0) AS actual_amount,
		       COALESCE((SELECT SUM(m.total_cost) FROM construction_sub_stage_materials m
		                 JOIN construction_sub_stages ss ON ss.id = m.sub_stage_id
		                 WHERE ss.stage_id = s.id), 0) AS material_total,
		       COALESCE((SELECT SUM(eq.plan_quantity * eq.unit_price) FROM construction_sub_stage_equipment eq
		                 JOIN construction_sub_stages ss ON ss.id = eq.sub_stage_id
		                 WHERE ss.stage_id = s.id AND COALESCE(eq.resource_type,'equipment') = 'equipment'), 0) AS equipment_total,
		       COALESCE((SELECT COUNT(*) FROM construction_sub_stage_equipment eq
		                 JOIN construction_sub_stages ss ON ss.id = eq.sub_stage_id
		                 WHERE ss.stage_id = s.id AND COALESCE(eq.resource_type,'equipment') = 'equipment'), 0) AS equipment_count,
		       COALESCE((SELECT SUM(eq.plan_quantity * eq.unit_price) FROM construction_sub_stage_equipment eq
		                 JOIN construction_sub_stages ss ON ss.id = eq.sub_stage_id
		                 WHERE ss.stage_id = s.id AND eq.resource_type = 'employee'), 0) AS labor_total,
		       COALESCE((SELECT COUNT(*) FROM construction_sub_stage_equipment eq
		                 JOIN construction_sub_stages ss ON ss.id = eq.sub_stage_id
		                 WHERE ss.stage_id = s.id AND eq.resource_type = 'employee'), 0) AS labor_count
		FROM construction_stages s
		LEFT JOIN construction_expense_lines el ON el.stage_id = s.id AND el.deleted_at IS NULL
		LEFT JOIN construction_buildings b ON b.id = s.building_id
		WHERE s.project_id = $1 AND s.tenant_id = $2` + buildingClause + `
		GROUP BY s.id, b.name
		ORDER BY s.stage_order ASC, s.id ASC
	`

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list stages", "error", err)
		response.InternalError(c, "Failed to list stages")
		return
	}
	defer rows.Close()

	type Stage struct {
		ID             int64     `json:"id"`
		TenantID       string    `json:"tenant_id"`
		ProjectID      int64     `json:"project_id"`
		BuildingID     *int64    `json:"building_id"`
		BuildingName   *string   `json:"building_name"`
		Name           string    `json:"name"`
		StageOrder     int       `json:"stage_order"`
		Status         string    `json:"status"`
		PlannedBudget  float64   `json:"planned_budget"`
		PlannedStart   *string   `json:"planned_start"`
		PlannedEnd     *string   `json:"planned_end"`
		ActualStart    *string   `json:"actual_start"`
		ActualEnd      *string   `json:"actual_end"`
		Notes          *string   `json:"notes"`
		CreatedAt      time.Time `json:"created_at"`
		UpdatedAt      time.Time `json:"updated_at"`
		ActualAmount   float64   `json:"actual_amount"`
		MaterialTotal  float64   `json:"material_total"`
		EquipmentTotal float64   `json:"equipment_total"`
		EquipmentCount int       `json:"equipment_count"`
		LaborTotal     float64   `json:"labor_total"`
		LaborCount     int       `json:"labor_count"`
	}

	stages := []Stage{}
	for rows.Next() {
		var s Stage
		var bID sql.NullInt64
		var bName sql.NullString
		if err := rows.Scan(
			&s.ID, &s.TenantID, &s.ProjectID, &bID, &s.Name, &s.StageOrder,
			&s.Status, &s.PlannedBudget, &s.PlannedStart, &s.PlannedEnd,
			&s.ActualStart, &s.ActualEnd, &s.Notes, &s.CreatedAt, &s.UpdatedAt,
			&bName,
			&s.ActualAmount, &s.MaterialTotal,
			&s.EquipmentTotal, &s.EquipmentCount, &s.LaborTotal, &s.LaborCount,
		); err != nil {
			h.log.Error("Failed to scan stage", "error", err)
			continue
		}
		if bID.Valid {
			v := bID.Int64
			s.BuildingID = &v
		}
		if bName.Valid {
			v := bName.String
			s.BuildingName = &v
		}
		stages = append(stages, s)
	}

	response.Success(c, stages)
}

// CreateConstructionStage creates a new stage for a project
func (h *Handler) CreateConstructionStage(c *gin.Context) {
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
		Name          string  `json:"name" binding:"required"`
		StageOrder    int     `json:"stage_order"`
		Status        string  `json:"status"`
		PlannedBudget float64 `json:"planned_budget"`
		PlannedStart  string  `json:"planned_start"`
		PlannedEnd    string  `json:"planned_end"`
		Notes         string  `json:"notes"`
		// Migration 333: stages can be assigned to a specific building/block.
		// 0 or absent → project-wide (unassigned), same as before.
		BuildingID int64 `json:"building_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	if req.Status == "" {
		req.Status = "not_started"
	}

	var buildingIDArg interface{}
	if req.BuildingID > 0 {
		buildingIDArg = req.BuildingID
	}

	var id int64
	err = h.db.QueryRow(`
		INSERT INTO construction_stages (
			tenant_id, project_id, building_id, name, stage_order, status, planned_budget,
			planned_start, planned_end, notes, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW(), NOW())
		RETURNING id
	`,
		tenantID, projectID, buildingIDArg, req.Name, req.StageOrder, req.Status, req.PlannedBudget,
		nullStringFromVal(req.PlannedStart), nullStringFromVal(req.PlannedEnd),
		nullStringFromVal(req.Notes),
	).Scan(&id)
	if err != nil {
		h.log.Error("Failed to create stage", "error", err)
		response.InternalError(c, "Failed to create stage")
		return
	}

	userID, _ := middleware.GetUserID(c)
	h.logConstructionActivity(tenantID, projectID, userID, "stage", fmt.Sprintf("Bosqich yaratildi: %s", req.Name), "Stage", id)

	response.Success(c, map[string]interface{}{"id": id, "message": "Stage created successfully"})
}

// UpdateConstructionStage updates an existing stage
func (h *Handler) UpdateConstructionStage(c *gin.Context) {
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

	var req struct {
		Name          *string  `json:"name"`
		StageOrder    *int     `json:"stage_order"`
		Status        *string  `json:"status"`
		PlannedBudget *float64 `json:"planned_budget"`
		PlannedStart  *string  `json:"planned_start"`
		PlannedEnd    *string  `json:"planned_end"`
		ActualStart   *string  `json:"actual_start"`
		ActualEnd     *string  `json:"actual_end"`
		Notes         *string  `json:"notes"`
		// Migration 333. Send 0 to clear the assignment (back to project-wide);
		// omit the field to leave it unchanged.
		BuildingID *int64 `json:"building_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	updates := []string{}
	args := []interface{}{}
	argCount := 0

	addField := func(col string, val interface{}) {
		argCount++
		updates = append(updates, fmt.Sprintf("%s = $%d", col, argCount))
		args = append(args, val)
	}

	if req.Name != nil {
		addField("name", *req.Name)
	}
	if req.StageOrder != nil {
		addField("stage_order", *req.StageOrder)
	}
	if req.Status != nil {
		addField("status", *req.Status)
		// Stamp actual_start the first time a stage transitions into
		// in_progress / completed, unless the request already supplies an
		// explicit actual_start. COALESCE preserves any previously-recorded
		// date, so toggling status back and forth doesn't reset it.
		if req.ActualStart == nil && (*req.Status == "in_progress" || *req.Status == "completed") {
			argCount++
			updates = append(updates, fmt.Sprintf("actual_start = COALESCE(actual_start, $%d)", argCount))
			args = append(args, time.Now().Format("2006-01-02"))
		}
		// Stamp actual_end the first time a stage is marked completed.
		if req.ActualEnd == nil && *req.Status == "completed" {
			argCount++
			updates = append(updates, fmt.Sprintf("actual_end = COALESCE(actual_end, $%d)", argCount))
			args = append(args, time.Now().Format("2006-01-02"))
		}
	}
	if req.PlannedBudget != nil {
		addField("planned_budget", *req.PlannedBudget)
	}
	if req.PlannedStart != nil {
		if *req.PlannedStart == "" {
			addField("planned_start", nil)
		} else {
			addField("planned_start", *req.PlannedStart)
		}
	}
	if req.PlannedEnd != nil {
		if *req.PlannedEnd == "" {
			addField("planned_end", nil)
		} else {
			addField("planned_end", *req.PlannedEnd)
		}
	}
	if req.ActualStart != nil {
		if *req.ActualStart == "" {
			addField("actual_start", nil)
		} else {
			addField("actual_start", *req.ActualStart)
		}
	}
	if req.ActualEnd != nil {
		if *req.ActualEnd == "" {
			addField("actual_end", nil)
		} else {
			addField("actual_end", *req.ActualEnd)
		}
	}
	if req.Notes != nil {
		if *req.Notes == "" {
			addField("notes", nil)
		} else {
			addField("notes", *req.Notes)
		}
	}
	if req.BuildingID != nil {
		if *req.BuildingID == 0 {
			addField("building_id", nil)
		} else {
			addField("building_id", *req.BuildingID)
		}
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(
		"UPDATE construction_stages SET %s WHERE id = $%d AND tenant_id = $%d",
		strings.Join(updates, ", "), argCount-1, argCount,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update stage", "error", err)
		response.InternalError(c, "Failed to update stage")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Stage not found")
		return
	}

	response.Success(c, map[string]interface{}{"id": id, "message": "Stage updated successfully"})
}

// =====================================================
// CONSTRUCTION SUB-STAGES HANDLERS
// =====================================================

// ListConstructionSubStages returns all sub-stages for a stage
func (h *Handler) ListConstructionSubStages(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	stageID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid stage ID")
		return
	}

	type SubStage struct {
		ID             int64     `json:"id"`
		StageID        int64     `json:"stage_id"`
		Name           string    `json:"name"`
		SubOrder       int       `json:"sub_order"`
		Status         string    `json:"status"`
		Notes          *string   `json:"notes"`
		CreatedAt      time.Time `json:"created_at"`
		UpdatedAt      time.Time `json:"updated_at"`
		MaterialCount  int       `json:"material_count"`
		MaterialTotal  float64   `json:"material_total"`
		EquipmentCount int       `json:"equipment_count"`
		EquipmentTotal float64   `json:"equipment_total"`
		LaborCount     int       `json:"labor_count"`
		LaborTotal     float64   `json:"labor_total"`
	}

	rows, err := h.db.Query(`
		SELECT ss.id, ss.stage_id, ss.name, ss.sub_order, ss.status, ss.notes, ss.created_at, ss.updated_at,
		       COALESCE((SELECT COUNT(*) FROM construction_sub_stage_materials m WHERE m.sub_stage_id = ss.id), 0) as material_count,
		       COALESCE((SELECT SUM(m.total_cost) FROM construction_sub_stage_materials m WHERE m.sub_stage_id = ss.id), 0) as material_total,
		       COALESCE((SELECT COUNT(*) FROM construction_sub_stage_equipment eq WHERE eq.sub_stage_id = ss.id AND COALESCE(eq.resource_type,'equipment') = 'equipment'), 0) as equipment_count,
		       COALESCE((SELECT SUM(eq.plan_quantity * eq.unit_price) FROM construction_sub_stage_equipment eq WHERE eq.sub_stage_id = ss.id AND COALESCE(eq.resource_type,'equipment') = 'equipment'), 0) as equipment_total,
		       COALESCE((SELECT COUNT(*) FROM construction_sub_stage_equipment eq WHERE eq.sub_stage_id = ss.id AND eq.resource_type = 'employee'), 0) as labor_count,
		       COALESCE((SELECT SUM(eq.plan_quantity * eq.unit_price) FROM construction_sub_stage_equipment eq WHERE eq.sub_stage_id = ss.id AND eq.resource_type = 'employee'), 0) as labor_total
		FROM construction_sub_stages ss
		WHERE ss.stage_id = $1 AND ss.tenant_id = $2
		ORDER BY ss.sub_order ASC, ss.id ASC
	`, stageID, tenantID)
	if err != nil {
		h.log.Error("Failed to list sub-stages", "error", err)
		response.InternalError(c, "Failed to list sub-stages")
		return
	}
	defer rows.Close()

	subStages := []SubStage{}
	for rows.Next() {
		var s SubStage
		if err := rows.Scan(
			&s.ID, &s.StageID, &s.Name, &s.SubOrder, &s.Status, &s.Notes, &s.CreatedAt, &s.UpdatedAt,
			&s.MaterialCount, &s.MaterialTotal,
			&s.EquipmentCount, &s.EquipmentTotal,
			&s.LaborCount, &s.LaborTotal,
		); err != nil {
			continue
		}
		subStages = append(subStages, s)
	}

	response.Success(c, subStages)
}

// CreateConstructionSubStage creates a new sub-stage for a stage
func (h *Handler) CreateConstructionSubStage(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	stageID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid stage ID")
		return
	}

	var req struct {
		Name     string `json:"name" binding:"required"`
		SubOrder int    `json:"sub_order"`
		Status   string `json:"status"`
		Notes    string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	if req.Status == "" {
		req.Status = "not_started"
	}

	var id int64
	err = h.db.QueryRow(`
		INSERT INTO construction_sub_stages (tenant_id, stage_id, name, sub_order, status, notes, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
		RETURNING id
	`, tenantID, stageID, req.Name, req.SubOrder, req.Status, nullStringFromVal(req.Notes)).Scan(&id)
	if err != nil {
		h.log.Error("Failed to create sub-stage", "error", err)
		response.InternalError(c, "Failed to create sub-stage")
		return
	}

	response.Success(c, map[string]interface{}{"id": id, "message": "Sub-stage created successfully"})
}

// UpdateConstructionSubStage updates an existing sub-stage
func (h *Handler) UpdateConstructionSubStage(c *gin.Context) {
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

	var req struct {
		Name     *string `json:"name"`
		SubOrder *int    `json:"sub_order"`
		Status   *string `json:"status"`
		Notes    *string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	updates := []string{}
	args := []interface{}{}
	argCount := 0

	addField := func(col string, val interface{}) {
		argCount++
		updates = append(updates, fmt.Sprintf("%s = $%d", col, argCount))
		args = append(args, val)
	}

	if req.Name != nil {
		addField("name", *req.Name)
	}
	if req.SubOrder != nil {
		addField("sub_order", *req.SubOrder)
	}
	if req.Status != nil {
		addField("status", *req.Status)
		// Stamp actual_start / actual_end on the matching status transitions.
		// Columns were added by migration 335; COALESCE preserves any value
		// already on record if the status is toggled multiple times.
		if *req.Status == "in_progress" || *req.Status == "completed" {
			argCount++
			updates = append(updates, fmt.Sprintf("actual_start = COALESCE(actual_start, $%d)", argCount))
			args = append(args, time.Now().Format("2006-01-02"))
		}
		if *req.Status == "completed" {
			argCount++
			updates = append(updates, fmt.Sprintf("actual_end = COALESCE(actual_end, $%d)", argCount))
			args = append(args, time.Now().Format("2006-01-02"))
		}
	}
	if req.Notes != nil {
		if *req.Notes == "" {
			addField("notes", nil)
		} else {
			addField("notes", *req.Notes)
		}
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(
		"UPDATE construction_sub_stages SET %s WHERE id = $%d AND tenant_id = $%d",
		strings.Join(updates, ", "), argCount-1, argCount,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update sub-stage", "error", err)
		response.InternalError(c, "Failed to update sub-stage")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Sub-stage not found")
		return
	}

	response.Success(c, map[string]interface{}{"id": id, "message": "Sub-stage updated successfully"})
}

// DeleteConstructionSubStage deletes a sub-stage
func (h *Handler) DeleteConstructionSubStage(c *gin.Context) {
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

	result, err := h.db.Exec(
		`DELETE FROM construction_sub_stages WHERE id = $1 AND tenant_id = $2`,
		id, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to delete sub-stage", "error", err)
		response.InternalError(c, "Failed to delete sub-stage")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Sub-stage not found")
		return
	}

	response.Success(c, map[string]interface{}{"message": "Sub-stage deleted successfully"})
}

// DeleteConstructionStage deletes a stage (only if no expense lines linked)
func (h *Handler) DeleteConstructionStage(c *gin.Context) {
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

	var expenseCount int
	_ = h.db.QueryRow(
		`SELECT COUNT(*) FROM construction_expense_lines WHERE stage_id = $1 AND deleted_at IS NULL`,
		id,
	).Scan(&expenseCount)
	if expenseCount > 0 {
		response.BadRequest(c, fmt.Sprintf("Cannot delete stage with %d expense line(s). Remove expenses first.", expenseCount))
		return
	}

	result, err := h.db.Exec(
		`DELETE FROM construction_stages WHERE id = $1 AND tenant_id = $2`,
		id, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to delete stage", "error", err)
		response.InternalError(c, "Failed to delete stage")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Stage not found")
		return
	}

	response.Success(c, map[string]interface{}{"message": "Stage deleted successfully"})
}

// ─────────────────────────────────────────────────────────────────────────────
// GetProjectInProgressItems
//
// Returns a flat feed of every construction item that is currently
// `status = 'in_progress'` for a given project — stages and sub-stages today,
// pluggable for more sources later. Used by the "Jarayon" tab in the frontend.
//
// Sub-stages don't have their own planned/actual dates, so they inherit the
// parent stage's dates for reporting purposes. `progress_pct` is derived from
// the elapsed fraction of the planned window; `bucket` classifies each row
// as "on_track" (actual_start ≤ today ≤ planned_end) or "behind" (planned_end
// has passed).
// ─────────────────────────────────────────────────────────────────────────────
func (h *Handler) GetProjectInProgressItems(c *gin.Context) {
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

	// Additional columns beyond the previous version:
	//   - sub_total / sub_done: counts of sub-stages and completed sub-stages
	//     under each row. For a stage row we use its own counts; for a
	//     sub-stage row we report 1 / (1 if completed else 0) so the
	//     downstream progress logic is uniform. Drives the progress_pct
	//     that the Jarayon tab renders (matches the Bosqichlar tab's
	//     "completed/total" ratio instead of the old time-elapsed estimate).
	//   - Sub-stage actual_start / actual_end (migration 335) fall back to
	//     the parent stage's dates so legacy rows still report something.
	//
	// Union arm 3 — works (construction_estimate_line) whose v2 approval
	// workflow is mid-flight. The user wants the Jarayon tab to surface
	// everything that isn't pending (draft) or confirmed_engineer
	// (final/completed); so we list works in `in_progress`, `submitted`,
	// or `confirmed_supervisor`. These are the works currently moving
	// through the foreman → supervisor → engineer pipeline.
	const q = `
		SELECT 'stage'::text AS source,
		       s.id,
		       NULL::bigint AS parent_id,
		       NULL::text   AS parent_name,
		       s.name,
		       s.status,
		       s.planned_start, s.planned_end,
		       s.actual_start,  s.actual_end,
		       b.name AS building_name,
		       COALESCE((SELECT COUNT(*)
		                 FROM construction_sub_stages x
		                 WHERE x.stage_id = s.id AND x.tenant_id = s.tenant_id), 0) AS sub_total,
		       COALESCE((SELECT COUNT(*)
		                 FROM construction_sub_stages x
		                 WHERE x.stage_id = s.id AND x.tenant_id = s.tenant_id
		                   AND x.status = 'completed'), 0) AS sub_done
		FROM construction_stages s
		LEFT JOIN construction_buildings b ON b.id = s.building_id
		WHERE s.project_id = $1 AND s.tenant_id = $2 AND s.status = 'in_progress'

		UNION ALL

		SELECT 'sub_stage'::text AS source,
		       ss.id,
		       s.id   AS parent_id,
		       s.name AS parent_name,
		       ss.name,
		       ss.status,
		       s.planned_start, s.planned_end,
		       COALESCE(ss.actual_start, s.actual_start) AS actual_start,
		       COALESCE(ss.actual_end,   s.actual_end)   AS actual_end,
		       b.name AS building_name,
		       1 AS sub_total,
		       (CASE WHEN ss.status = 'completed' THEN 1 ELSE 0 END) AS sub_done
		FROM construction_sub_stages ss
		JOIN construction_stages s ON s.id = ss.stage_id
		LEFT JOIN construction_buildings b ON b.id = s.building_id
		WHERE s.project_id = $1 AND s.tenant_id = $2 AND ss.status = 'in_progress'

		UNION ALL

		-- v2 approval-workflow works that are mid-pipeline.
		--   in_progress           — foreman has entered qty but not submitted
		--   submitted             — waiting on supervisor
		--   confirmed_supervisor  — waiting on chief engineer's final OK
		-- pending and confirmed_engineer are intentionally excluded —
		-- pending == not started yet (draft), confirmed_engineer == done.
		SELECT 'work'::text AS source,
		       el.id,
		       NULL::bigint AS parent_id,
		       NULLIF(el.parent_item_number, '') AS parent_name,
		       el.name,
		       el.approval_status AS status,
		       NULL::date AS planned_start,
		       NULL::date AS planned_end,
		       NULL::date AS actual_start,
		       NULL::date AS actual_end,
		       b.name AS building_name,
		       -- Use plan/done quantity as the progress source. Mapping
		       -- onto the existing sub_total/sub_done columns lets the
		       -- downstream code path stay uniform with stages.
		       (CASE WHEN el.quantity > 0 THEN 100 ELSE 0 END)::bigint AS sub_total,
		       (CASE
		          WHEN el.quantity > 0
		            THEN LEAST(100, ROUND(COALESCE(el.done_quantity, 0) / el.quantity * 100))
		          ELSE 0
		        END)::bigint AS sub_done
		FROM construction_estimate_line el
		JOIN construction_estimate e ON e.id = el.estimate_id
		LEFT JOIN construction_buildings b ON b.id = e.building_id
		WHERE e.project_id = $1
		  AND el.tenant_id = $2
		  AND el.parent_line_id IS NULL                       -- top-level works only
		  AND COALESCE(el.resource_type, '') = ''             -- skip resource lines
		  AND el.approval_status IN ('in_progress', 'submitted', 'confirmed_supervisor')

		ORDER BY source, name
	`
	rows, err := h.db.Query(q, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to query in-progress items", "error", err)
		response.InternalError(c, "Failed to load in-progress items")
		return
	}
	defer rows.Close()

	type Item struct {
		Source       string   `json:"source"`
		ID           int64    `json:"id"`
		ParentID     *int64   `json:"parent_id"`
		ParentName   *string  `json:"parent_name"`
		Name         string   `json:"name"`
		Status       string   `json:"status"`
		PlannedStart *string  `json:"planned_start"`
		PlannedEnd   *string  `json:"planned_end"`
		ActualStart  *string  `json:"actual_start"`
		ActualEnd    *string  `json:"actual_end"`
		BuildingName *string  `json:"building_name"`
		ProgressPct  float64  `json:"progress_pct"`
		Bucket       string   `json:"bucket"` // on_track | behind
	}

	now := time.Now()
	items := []Item{}

	for rows.Next() {
		var it Item
		var parentID sql.NullInt64
		var parentName, buildingName sql.NullString
		var plannedStartT, plannedEndT, actualStartT, actualEndT sql.NullTime
		var subTotal, subDone int64
		if err := rows.Scan(
			&it.Source, &it.ID, &parentID, &parentName,
			&it.Name, &it.Status,
			&plannedStartT, &plannedEndT,
			&actualStartT, &actualEndT,
			&buildingName,
			&subTotal, &subDone,
		); err != nil {
			h.log.Error("Failed to scan in-progress item", "error", err)
			continue
		}
		if parentID.Valid {
			v := parentID.Int64
			it.ParentID = &v
		}
		if parentName.Valid {
			v := parentName.String
			it.ParentName = &v
		}
		if plannedStartT.Valid {
			v := plannedStartT.Time.Format("2006-01-02")
			it.PlannedStart = &v
		}
		if plannedEndT.Valid {
			v := plannedEndT.Time.Format("2006-01-02")
			it.PlannedEnd = &v
		}
		if actualStartT.Valid {
			v := actualStartT.Time.Format("2006-01-02")
			it.ActualStart = &v
		}
		if actualEndT.Valid {
			v := actualEndT.Time.Format("2006-01-02")
			it.ActualEnd = &v
		}
		if buildingName.Valid {
			v := buildingName.String
			it.BuildingName = &v
		}

		// Progress policy:
		//   * stage rows → ratio of completed sub-stages (matches the
		//     Bosqichlar tab's "1/2 = 50%" display).
		//   * stage rows with no sub-stages → 0% when in progress, 100% when
		//     completed. (This feed only returns in_progress, so effectively 0.)
		//   * sub-stage rows → 0/50/100 based on their own status.
		//   * work rows (v2 approval workflow) → done_quantity / quantity *
		//     100, with a small bump for the "submitted" /
		//     "confirmed_supervisor" stages so works that already passed
		//     a review step never read as 0% even when the foreman didn't
		//     bother filling in a quantity. The SQL feeds sub_total = 100
		//     and sub_done = ratio*100 already, so this is just division.
		switch it.Source {
		case "stage":
			if subTotal > 0 {
				it.ProgressPct = float64(subDone) / float64(subTotal) * 100
			} else if it.Status == "completed" {
				it.ProgressPct = 100
			} else {
				it.ProgressPct = 0
			}
		case "sub_stage":
			switch it.Status {
			case "completed":
				it.ProgressPct = 100
			case "in_progress":
				it.ProgressPct = 50
			default:
				it.ProgressPct = 0
			}
		case "work":
			if subTotal > 0 {
				it.ProgressPct = float64(subDone) / float64(subTotal) * 100
			}
			// Floor a few representative values so submitted / supervisor-
			// confirmed works never read as 0% even when the foreman left
			// the done quantity blank.
			switch it.Status {
			case "confirmed_supervisor":
				if it.ProgressPct < 90 {
					it.ProgressPct = 90
				}
			case "submitted":
				if it.ProgressPct < 75 {
					it.ProgressPct = 75
				}
			case "in_progress":
				if it.ProgressPct < 25 {
					it.ProgressPct = 25
				}
			}
		}

		// "Behind" only makes sense once a planned_end is on record and past.
		if plannedEndT.Valid && now.After(plannedEndT.Time) {
			it.Bucket = "behind"
		} else {
			it.Bucket = "on_track"
		}
		items = append(items, it)
	}

	// KPI rollup
	total := len(items)
	onTrack := 0
	behind := 0
	sumPct := 0.0
	for _, it := range items {
		if it.Bucket == "behind" {
			behind++
		} else {
			onTrack++
		}
		sumPct += it.ProgressPct
	}
	avgPct := 0.0
	if total > 0 {
		avgPct = sumPct / float64(total)
	}

	response.Success(c, map[string]interface{}{
		"project_id": projectID,
		"items":      items,
		"kpis": map[string]interface{}{
			"total":    total,
			"on_track": onTrack,
			"behind":   behind,
			"avg_pct":  avgPct,
		},
	})
}
