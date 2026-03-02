package handler

import (
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

	rows, err := h.db.Query(`
		SELECT s.id, s.tenant_id, s.project_id, s.name, s.stage_order,
		       s.status, s.planned_budget, s.planned_start, s.planned_end,
		       s.actual_start, s.actual_end, s.notes, s.created_at, s.updated_at,
		       COALESCE(SUM(CASE WHEN el.status = 'approved' AND el.deleted_at IS NULL THEN el.amount ELSE 0 END), 0) AS actual_amount
		FROM construction_stages s
		LEFT JOIN construction_expense_lines el ON el.stage_id = s.id AND el.deleted_at IS NULL
		WHERE s.project_id = $1 AND s.tenant_id = $2
		GROUP BY s.id
		ORDER BY s.stage_order ASC, s.id ASC
	`, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to list stages", "error", err)
		response.InternalError(c, "Failed to list stages")
		return
	}
	defer rows.Close()

	type Stage struct {
		ID            int64    `json:"id"`
		TenantID      string   `json:"tenant_id"`
		ProjectID     int64    `json:"project_id"`
		Name          string   `json:"name"`
		StageOrder    int      `json:"stage_order"`
		Status        string   `json:"status"`
		PlannedBudget float64  `json:"planned_budget"`
		PlannedStart  *string  `json:"planned_start"`
		PlannedEnd    *string  `json:"planned_end"`
		ActualStart   *string  `json:"actual_start"`
		ActualEnd     *string  `json:"actual_end"`
		Notes         *string  `json:"notes"`
		CreatedAt     time.Time `json:"created_at"`
		UpdatedAt     time.Time `json:"updated_at"`
		ActualAmount  float64  `json:"actual_amount"`
	}

	stages := []Stage{}
	for rows.Next() {
		var s Stage
		if err := rows.Scan(
			&s.ID, &s.TenantID, &s.ProjectID, &s.Name, &s.StageOrder,
			&s.Status, &s.PlannedBudget, &s.PlannedStart, &s.PlannedEnd,
			&s.ActualStart, &s.ActualEnd, &s.Notes, &s.CreatedAt, &s.UpdatedAt,
			&s.ActualAmount,
		); err != nil {
			h.log.Error("Failed to scan stage", "error", err)
			continue
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
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if req.Status == "" {
		req.Status = "not_started"
	}

	var id int64
	err = h.db.QueryRow(`
		INSERT INTO construction_stages (
			tenant_id, project_id, name, stage_order, status, planned_budget,
			planned_start, planned_end, notes, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())
		RETURNING id
	`,
		tenantID, projectID, req.Name, req.StageOrder, req.Status, req.PlannedBudget,
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
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
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
