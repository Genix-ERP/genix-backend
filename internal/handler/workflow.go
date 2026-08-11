package handler

import (
	"database/sql"
	"encoding/json"
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

// ListWorkflows returns a paginated list of workflows
func (h *Handler) ListWorkflows(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	offset := (page - 1) * limit

	// Parse filters
	category := c.Query("category")
	status := c.Query("status")
	search := c.Query("search")
	sortBy := c.DefaultQuery("sort_by", "created_at")
	sortOrder := c.DefaultQuery("sort_order", "DESC")

	// Build query
	baseQuery := `
		SELECT id, tenant_id, name, description, category, trigger,
			   status, automation_level, steps, success_rate,
			   avg_completion_time, cost_savings, created_at, updated_at
		FROM workflows
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM workflows WHERE tenant_id = $1 AND deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	// Apply filters
	if category != "" && category != "all" {
		argCount++
		filter := fmt.Sprintf(" AND category = $%d", argCount)
		baseQuery += filter
		countQuery += filter
		args = append(args, category)
	}

	if status != "" && status != "all" {
		argCount++
		filter := fmt.Sprintf(" AND status = $%d", argCount)
		baseQuery += filter
		countQuery += filter
		args = append(args, status)
	}

	if search != "" {
		argCount++
		searchFilter := fmt.Sprintf(" AND (name ILIKE $%d OR description ILIKE $%d)", argCount, argCount)
		baseQuery += searchFilter
		countQuery += searchFilter
		args = append(args, "%"+search+"%")
	}

	// Add sorting
	validSortFields := map[string]bool{
		"name": true, "created_at": true, "success_rate": true,
		"avg_completion_time": true, "cost_savings": true,
	}
	if !validSortFields[sortBy] {
		sortBy = "created_at"
	}
	if sortOrder != "ASC" && sortOrder != "DESC" {
		sortOrder = "DESC"
	}
	baseQuery += fmt.Sprintf(" ORDER BY %s %s, id ASC", sortBy, sortOrder)

	// Add pagination
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	// Get total count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count workflows", "error", err)
		response.InternalError(c, "Failed to count workflows")
		return
	}

	// Execute query
	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list workflows", "error", err)
		response.InternalError(c, "Failed to list workflows")
		return
	}
	defer rows.Close()

	workflows := make([]*entity.WorkflowResponse, 0)
	for rows.Next() {
		var wf entity.Workflow
		var description sql.NullString

		err := rows.Scan(
			&wf.ID, &wf.TenantID, &wf.Name, &description, &wf.Category, &wf.Trigger,
			&wf.Status, &wf.AutomationLevel, &wf.Steps, &wf.SuccessRate,
			&wf.AvgCompletionTime, &wf.CostSavings, &wf.CreatedAt, &wf.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan workflow", "error", err)
			continue
		}

		if description.Valid {
			wf.Description = &description.String
		}

		workflows = append(workflows, wf.ToResponse())
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, workflows, pagination)
}

// CreateWorkflow creates a new workflow
func (h *Handler) CreateWorkflow(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CreateWorkflowInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Default values
	trigger := input.Trigger
	if trigger == "" {
		trigger = "manual"
	}

	status := input.Status
	if status == "" {
		status = "draft"
	}

	automationLevel := input.AutomationLevel
	if automationLevel == "" {
		automationLevel = "manual"
	}

	// Marshal steps to JSON
	var stepsJSON []byte
	var err error
	if input.Steps != nil && len(input.Steps) > 0 {
		stepsJSON, err = json.Marshal(input.Steps)
		if err != nil {
			response.BadRequest(c, "Invalid steps data")
			return
		}
	} else {
		stepsJSON = []byte("[]")
	}

	id := uuid.New()
	now := time.Now()

	query := `
		INSERT INTO workflows (
			id, tenant_id, name, description, category, trigger,
			status, automation_level, steps, success_rate,
			avg_completion_time, cost_savings, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id, created_at
	`

	var description *string
	if input.Description != "" {
		description = &input.Description
	}

	err = h.db.QueryRow(query,
		id, tenantID, input.Name, description, input.Category, trigger,
		status, automationLevel, stepsJSON, input.SuccessRate,
		input.AvgCompletionTime, input.CostSavings, now, now,
	).Scan(&id, &now)

	if err != nil {
		h.log.Error("Failed to create workflow", "error", err)
		response.InternalError(c, "Failed to create workflow")
		return
	}

	wf := &entity.Workflow{
		ID:                id,
		TenantID:          tenantID,
		Name:              input.Name,
		Description:       description,
		Category:          input.Category,
		Trigger:           trigger,
		Status:            status,
		AutomationLevel:   automationLevel,
		Steps:             stepsJSON,
		SuccessRate:       input.SuccessRate,
		AvgCompletionTime: input.AvgCompletionTime,
		CostSavings:       input.CostSavings,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	response.Created(c, wf.ToResponse())
}

// GetWorkflow returns a single workflow by ID
func (h *Handler) GetWorkflow(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid workflow ID")
		return
	}

	query := `
		SELECT id, tenant_id, name, description, category, trigger,
			   status, automation_level, steps, success_rate,
			   avg_completion_time, cost_savings, created_at, updated_at
		FROM workflows
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`

	var wf entity.Workflow
	var description sql.NullString

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&wf.ID, &wf.TenantID, &wf.Name, &description, &wf.Category, &wf.Trigger,
		&wf.Status, &wf.AutomationLevel, &wf.Steps, &wf.SuccessRate,
		&wf.AvgCompletionTime, &wf.CostSavings, &wf.CreatedAt, &wf.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Workflow")
		return
	}
	if err != nil {
		h.log.Error("Failed to get workflow", "error", err)
		response.InternalError(c, "Failed to get workflow")
		return
	}

	if description.Valid {
		wf.Description = &description.String
	}

	response.Success(c, wf.ToResponse())
}

// UpdateWorkflow updates an existing workflow
func (h *Handler) UpdateWorkflow(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid workflow ID")
		return
	}

	var input entity.UpdateWorkflowInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build dynamic update query
	updates := make([]string, 0)
	args := make([]interface{}, 0)
	argCount := 0

	addUpdate := func(field string, value interface{}) {
		argCount++
		updates = append(updates, fmt.Sprintf("%s = $%d", field, argCount))
		args = append(args, value)
	}

	if input.Name != nil {
		addUpdate("name", *input.Name)
	}
	if input.Description != nil {
		addUpdate("description", *input.Description)
	}
	if input.Category != nil {
		addUpdate("category", *input.Category)
	}
	if input.Trigger != nil {
		addUpdate("trigger", *input.Trigger)
	}
	if input.Status != nil {
		addUpdate("status", *input.Status)
	}
	if input.AutomationLevel != nil {
		addUpdate("automation_level", *input.AutomationLevel)
	}
	if input.Steps != nil {
		stepsJSON, err := json.Marshal(*input.Steps)
		if err != nil {
			response.BadRequest(c, "Invalid steps data")
			return
		}
		addUpdate("steps", stepsJSON)
	}
	if input.SuccessRate != nil {
		addUpdate("success_rate", *input.SuccessRate)
	}
	if input.AvgCompletionTime != nil {
		addUpdate("avg_completion_time", *input.AvgCompletionTime)
	}
	if input.CostSavings != nil {
		addUpdate("cost_savings", *input.CostSavings)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	addUpdate("updated_at", time.Now())

	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(`
		UPDATE workflows SET %s
		WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
		RETURNING id
	`, strings.Join(updates, ", "), argCount-1, argCount)

	var returnedID uuid.UUID
	err = h.db.QueryRow(query, args...).Scan(&returnedID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Workflow")
		return
	}
	if err != nil {
		h.log.Error("Failed to update workflow", "error", err)
		response.InternalError(c, "Failed to update workflow")
		return
	}

	// Fetch updated record
	h.GetWorkflow(c)
}

// DeleteWorkflow soft-deletes a workflow
func (h *Handler) DeleteWorkflow(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid workflow ID")
		return
	}

	query := `
		UPDATE workflows SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`

	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete workflow", "error", err)
		response.InternalError(c, "Failed to delete workflow")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Workflow")
		return
	}

	response.NoContent(c)
}
