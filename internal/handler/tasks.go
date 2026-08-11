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

// =====================================================
// TASK HANDLERS
// =====================================================

// ListTasks returns a paginated list of tasks
func (h *Handler) ListTasks(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	offset := (page - 1) * limit

	// Parse filters
	search := c.Query("search")
	taskType := c.Query("task_type")
	status := c.Query("status")
	priority := c.Query("priority")
	leadID := c.Query("lead_id")
	contactID := c.Query("contact_id")
	opportunityID := c.Query("opportunity_id")
	assignedTo := c.Query("assigned_to")
	dueDateFrom := c.Query("due_date_from")
	dueDateTo := c.Query("due_date_to")
	includeCompleted := c.Query("include_completed") == "true"
	overdueOnly := c.Query("overdue_only") == "true"

	// Build query
	baseQuery := `
		SELECT t.id, t.tenant_id, t.title, t.description,
			   t.lead_id, t.contact_id, t.opportunity_id, t.activity_id,
			   t.task_type, t.priority, t.status, t.due_date, t.due_time,
			   t.completed_at, t.assigned_to, t.assigned_by,
			   t.progress_percent, t.estimated_hours, t.actual_hours,
			   t.is_recurring, t.recurrence_pattern, t.recurrence_end_date,
			   t.parent_task_id, t.tags, t.created_at, t.updated_at,
			   l.contact_name as lead_name,
			   ct.name as contact_name,
			   o.name as opportunity_name,
			   u1.first_name || ' ' || u1.last_name as assigned_to_name,
			   u2.first_name || ' ' || u2.last_name as assigned_by_name
		FROM crm_tasks t
		LEFT JOIN leads l ON t.lead_id = l.id
		LEFT JOIN contacts ct ON t.contact_id = ct.id
		LEFT JOIN opportunities o ON t.opportunity_id = o.id
		LEFT JOIN users u1 ON t.assigned_to = u1.id
		LEFT JOIN users u2 ON t.assigned_by = u2.id
		WHERE t.tenant_id = $1 AND t.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM crm_tasks t WHERE t.tenant_id = $1 AND t.deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	if !includeCompleted {
		baseQuery += " AND t.status NOT IN ('completed', 'cancelled')"
		countQuery += " AND t.status NOT IN ('completed', 'cancelled')"
	}

	if overdueOnly {
		baseQuery += " AND t.due_date < CURRENT_DATE AND t.status NOT IN ('completed', 'cancelled')"
		countQuery += " AND t.due_date < CURRENT_DATE AND t.status NOT IN ('completed', 'cancelled')"
	}

	if taskType != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND t.task_type = $%d", argCount)
		countQuery += fmt.Sprintf(" AND t.task_type = $%d", argCount)
		args = append(args, taskType)
	}

	if status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND t.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND t.status = $%d", argCount)
		args = append(args, status)
	}

	if priority != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND t.priority = $%d", argCount)
		countQuery += fmt.Sprintf(" AND t.priority = $%d", argCount)
		args = append(args, priority)
	}

	if leadID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND t.lead_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND t.lead_id = $%d", argCount)
		args = append(args, leadID)
	}

	if contactID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND t.contact_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND t.contact_id = $%d", argCount)
		args = append(args, contactID)
	}

	if opportunityID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND t.opportunity_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND t.opportunity_id = $%d", argCount)
		args = append(args, opportunityID)
	}

	if assignedTo != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND t.assigned_to = $%d", argCount)
		countQuery += fmt.Sprintf(" AND t.assigned_to = $%d", argCount)
		args = append(args, assignedTo)
	}

	if dueDateFrom != "" {
		if t, err := time.Parse("2006-01-02", dueDateFrom); err == nil {
			argCount++
			baseQuery += fmt.Sprintf(" AND t.due_date >= $%d", argCount)
			countQuery += fmt.Sprintf(" AND t.due_date >= $%d", argCount)
			args = append(args, t)
		}
	}

	if dueDateTo != "" {
		if t, err := time.Parse("2006-01-02", dueDateTo); err == nil {
			argCount++
			baseQuery += fmt.Sprintf(" AND t.due_date <= $%d", argCount)
			countQuery += fmt.Sprintf(" AND t.due_date <= $%d", argCount)
			args = append(args, t)
		}
	}

	if search != "" {
		argCount++
		searchFilter := fmt.Sprintf(" AND (t.title ILIKE $%d OR t.description ILIKE $%d)", argCount, argCount)
		baseQuery += searchFilter
		countQuery += searchFilter
		args = append(args, "%"+search+"%")
	}

	// Get count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count tasks", "error", err)
		response.InternalError(c, "Failed to list tasks")
		return
	}

	// Add ordering and pagination
	sortBy := c.DefaultQuery("sort_by", "due_date")
	sortOrder := c.DefaultQuery("sort_order", "ASC")
	if sortOrder != "ASC" && sortOrder != "DESC" {
		sortOrder = "ASC"
	}
	allowedSorts := map[string]bool{
		"due_date": true, "priority": true, "created_at": true, "title": true, "status": true,
	}
	if !allowedSorts[sortBy] {
		sortBy = "due_date"
	}
	baseQuery += fmt.Sprintf(" ORDER BY t.%s %s NULLS LAST, t.id ASC", sortBy, sortOrder)
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list tasks", "error", err)
		response.InternalError(c, "Failed to list tasks")
		return
	}
	defer rows.Close()

	tasks := make([]*entity.TaskResponse, 0)
	for rows.Next() {
		var t entity.Task
		var description, dueTime, recurrencePattern sql.NullString
		var leadID, contactID, opportunityID, activityID, assignedTo, assignedBy, parentTaskID sql.NullString
		var estimatedHours, actualHours sql.NullFloat64
		var dueDate, completedAt, recurrenceEndDate sql.NullTime
		var leadName, contactName, opportunityName, assignedToName, assignedByName sql.NullString
		var tags []byte

		err := rows.Scan(
			&t.ID, &t.TenantID, &t.Title, &description,
			&leadID, &contactID, &opportunityID, &activityID,
			&t.TaskType, &t.Priority, &t.Status, &dueDate, &dueTime,
			&completedAt, &assignedTo, &assignedBy,
			&t.ProgressPercent, &estimatedHours, &actualHours,
			&t.IsRecurring, &recurrencePattern, &recurrenceEndDate,
			&parentTaskID, &tags, &t.CreatedAt, &t.UpdatedAt,
			&leadName, &contactName, &opportunityName, &assignedToName, &assignedByName,
		)
		if err != nil {
			h.log.Error("Failed to scan task", "error", err)
			continue
		}

		resp := &entity.TaskResponse{
			ID:              t.ID,
			Title:           t.Title,
			TaskType:        t.TaskType,
			Priority:        t.Priority,
			Status:          t.Status,
			ProgressPercent: t.ProgressPercent,
			IsRecurring:     t.IsRecurring,
			Tags:            []string{},
			CreatedAt:       t.CreatedAt,
			UpdatedAt:       t.UpdatedAt,
		}

		if description.Valid {
			resp.Description = &description.String
		}
		if leadID.Valid {
			lid, _ := uuid.Parse(leadID.String)
			resp.LeadID = &lid
		}
		if leadName.Valid {
			resp.LeadName = &leadName.String
		}
		if contactID.Valid {
			cid, _ := uuid.Parse(contactID.String)
			resp.ContactID = &cid
		}
		if contactName.Valid {
			resp.ContactName = &contactName.String
		}
		if opportunityID.Valid {
			oid, _ := uuid.Parse(opportunityID.String)
			resp.OpportunityID = &oid
		}
		if opportunityName.Valid {
			resp.OpportunityName = &opportunityName.String
		}
		if dueDate.Valid {
			resp.DueDate = &dueDate.Time
		}
		if dueTime.Valid {
			resp.DueTime = &dueTime.String
		}
		if completedAt.Valid {
			resp.CompletedAt = &completedAt.Time
		}
		if assignedTo.Valid {
			aid, _ := uuid.Parse(assignedTo.String)
			resp.AssignedTo = &aid
		}
		if assignedToName.Valid {
			resp.AssignedToName = &assignedToName.String
		}
		if assignedBy.Valid {
			abid, _ := uuid.Parse(assignedBy.String)
			resp.AssignedBy = &abid
		}
		if assignedByName.Valid {
			resp.AssignedByName = &assignedByName.String
		}
		if estimatedHours.Valid {
			resp.EstimatedHours = &estimatedHours.Float64
		}
		if actualHours.Valid {
			resp.ActualHours = &actualHours.Float64
		}
		if recurrencePattern.Valid {
			resp.RecurrencePattern = &recurrencePattern.String
		}
		if len(tags) > 0 {
			json.Unmarshal(tags, &resp.Tags)
		}

		// Check if overdue
		if dueDate.Valid && t.Status != entity.TaskStatusCompleted && t.Status != entity.TaskStatusCancelled {
			resp.IsOverdue = dueDate.Time.Before(time.Now().Truncate(24 * time.Hour))
		}

		tasks = append(tasks, resp)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, tasks, pagination)
}

// CreateTask creates a new task
func (h *Handler) CreateTask(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateTaskInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	id := uuid.New()
	now := time.Now()

	// Parse optional UUIDs
	var leadID, contactID, opportunityID, activityID, assignedTo, parentTaskID *uuid.UUID
	if input.LeadID != "" {
		lid, err := uuid.Parse(input.LeadID)
		if err == nil {
			leadID = &lid
		}
	}
	if input.ContactID != "" {
		cid, err := uuid.Parse(input.ContactID)
		if err == nil {
			contactID = &cid
		}
	}
	if input.OpportunityID != "" {
		oid, err := uuid.Parse(input.OpportunityID)
		if err == nil {
			opportunityID = &oid
		}
	}
	if input.ActivityID != "" {
		aid, err := uuid.Parse(input.ActivityID)
		if err == nil {
			activityID = &aid
		}
	}
	if input.AssignedTo != "" {
		aid, err := uuid.Parse(input.AssignedTo)
		if err == nil {
			assignedTo = &aid
		}
	}
	if input.ParentTaskID != "" {
		pid, err := uuid.Parse(input.ParentTaskID)
		if err == nil {
			parentTaskID = &pid
		}
	}

	// Parse dates
	var dueDate, recurrenceEndDate *time.Time
	if input.DueDate != "" {
		if t, err := time.Parse("2006-01-02", input.DueDate); err == nil {
			dueDate = &t
		}
	}
	if input.RecurrenceEndDate != "" {
		if t, err := time.Parse("2006-01-02", input.RecurrenceEndDate); err == nil {
			recurrenceEndDate = &t
		}
	}

	// Set defaults
	taskType := entity.TaskTypeGeneral
	if input.TaskType != "" {
		taskType = entity.TaskType(input.TaskType)
	}

	priority := entity.TaskPriorityMedium
	if input.Priority != "" {
		priority = entity.TaskPriority(input.Priority)
	}

	status := entity.TaskStatusPending
	if input.Status != "" {
		status = entity.TaskStatus(input.Status)
	}

	// Serialize tags
	tags, _ := json.Marshal(input.Tags)
	if input.Tags == nil {
		tags = []byte("[]")
	}

	// Prepare optional strings
	var description, dueTime, recurrencePattern *string
	if input.Description != "" {
		description = &input.Description
	}
	if input.DueTime != "" {
		dueTime = &input.DueTime
	}
	if input.RecurrencePattern != "" {
		recurrencePattern = &input.RecurrencePattern
	}

	query := `
		INSERT INTO crm_tasks (
			id, tenant_id, title, description,
			lead_id, contact_id, opportunity_id, activity_id,
			task_type, priority, status, due_date, due_time,
			assigned_to, assigned_by, estimated_hours,
			is_recurring, recurrence_pattern, recurrence_end_date,
			parent_task_id, tags, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)
		RETURNING id
	`

	err := h.db.QueryRow(query,
		id, tenantID, input.Title, description,
		leadID, contactID, opportunityID, activityID,
		taskType, priority, status, dueDate, dueTime,
		assignedTo, userID, input.EstimatedHours,
		input.IsRecurring, recurrencePattern, recurrenceEndDate,
		parentTaskID, tags, userID, now, now,
	).Scan(&id)

	if err != nil {
		h.log.Error("Failed to create task", "error", err)
		response.InternalError(c, "Failed to create task")
		return
	}

	resp := &entity.TaskResponse{
		ID:                id,
		Title:             input.Title,
		Description:       description,
		LeadID:            leadID,
		ContactID:         contactID,
		OpportunityID:     opportunityID,
		TaskType:          taskType,
		Priority:          priority,
		Status:            status,
		DueDate:           dueDate,
		DueTime:           dueTime,
		AssignedTo:        assignedTo,
		AssignedBy:        &userID,
		EstimatedHours:    input.EstimatedHours,
		IsRecurring:       input.IsRecurring,
		RecurrencePattern: recurrencePattern,
		Tags:              input.Tags,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	response.Created(c, resp)
}

// GetTask returns a single task by ID
func (h *Handler) GetTask(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid task ID")
		return
	}

	query := `
		SELECT t.id, t.tenant_id, t.title, t.description,
			   t.lead_id, t.contact_id, t.opportunity_id, t.activity_id,
			   t.task_type, t.priority, t.status, t.due_date, t.due_time,
			   t.completed_at, t.assigned_to, t.assigned_by,
			   t.progress_percent, t.estimated_hours, t.actual_hours,
			   t.is_recurring, t.recurrence_pattern, t.recurrence_end_date,
			   t.parent_task_id, t.tags, t.created_at, t.updated_at,
			   l.contact_name as lead_name,
			   ct.name as contact_name,
			   o.name as opportunity_name,
			   u1.first_name || ' ' || u1.last_name as assigned_to_name,
			   u2.first_name || ' ' || u2.last_name as assigned_by_name
		FROM crm_tasks t
		LEFT JOIN leads l ON t.lead_id = l.id
		LEFT JOIN contacts ct ON t.contact_id = ct.id
		LEFT JOIN opportunities o ON t.opportunity_id = o.id
		LEFT JOIN users u1 ON t.assigned_to = u1.id
		LEFT JOIN users u2 ON t.assigned_by = u2.id
		WHERE t.id = $1 AND t.tenant_id = $2 AND t.deleted_at IS NULL
	`

	var t entity.Task
	var description, dueTime, recurrencePattern sql.NullString
	var leadID, contactID, opportunityID, activityID, assignedTo, assignedBy, parentTaskID sql.NullString
	var estimatedHours, actualHours sql.NullFloat64
	var dueDate, completedAt, recurrenceEndDate sql.NullTime
	var leadName, contactName, opportunityName, assignedToName, assignedByName sql.NullString
	var tags []byte

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&t.ID, &t.TenantID, &t.Title, &description,
		&leadID, &contactID, &opportunityID, &activityID,
		&t.TaskType, &t.Priority, &t.Status, &dueDate, &dueTime,
		&completedAt, &assignedTo, &assignedBy,
		&t.ProgressPercent, &estimatedHours, &actualHours,
		&t.IsRecurring, &recurrencePattern, &recurrenceEndDate,
		&parentTaskID, &tags, &t.CreatedAt, &t.UpdatedAt,
		&leadName, &contactName, &opportunityName, &assignedToName, &assignedByName,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Task")
		return
	}
	if err != nil {
		h.log.Error("Failed to get task", "error", err)
		response.InternalError(c, "Failed to get task")
		return
	}

	resp := &entity.TaskResponse{
		ID:              t.ID,
		Title:           t.Title,
		TaskType:        t.TaskType,
		Priority:        t.Priority,
		Status:          t.Status,
		ProgressPercent: t.ProgressPercent,
		IsRecurring:     t.IsRecurring,
		Tags:            []string{},
		CreatedAt:       t.CreatedAt,
		UpdatedAt:       t.UpdatedAt,
	}

	if description.Valid {
		resp.Description = &description.String
	}
	if leadID.Valid {
		lid, _ := uuid.Parse(leadID.String)
		resp.LeadID = &lid
	}
	if leadName.Valid {
		resp.LeadName = &leadName.String
	}
	if contactID.Valid {
		cid, _ := uuid.Parse(contactID.String)
		resp.ContactID = &cid
	}
	if contactName.Valid {
		resp.ContactName = &contactName.String
	}
	if opportunityID.Valid {
		oid, _ := uuid.Parse(opportunityID.String)
		resp.OpportunityID = &oid
	}
	if opportunityName.Valid {
		resp.OpportunityName = &opportunityName.String
	}
	if dueDate.Valid {
		resp.DueDate = &dueDate.Time
	}
	if dueTime.Valid {
		resp.DueTime = &dueTime.String
	}
	if completedAt.Valid {
		resp.CompletedAt = &completedAt.Time
	}
	if assignedTo.Valid {
		aid, _ := uuid.Parse(assignedTo.String)
		resp.AssignedTo = &aid
	}
	if assignedToName.Valid {
		resp.AssignedToName = &assignedToName.String
	}
	if assignedBy.Valid {
		abid, _ := uuid.Parse(assignedBy.String)
		resp.AssignedBy = &abid
	}
	if assignedByName.Valid {
		resp.AssignedByName = &assignedByName.String
	}
	if estimatedHours.Valid {
		resp.EstimatedHours = &estimatedHours.Float64
	}
	if actualHours.Valid {
		resp.ActualHours = &actualHours.Float64
	}
	if recurrencePattern.Valid {
		resp.RecurrencePattern = &recurrencePattern.String
	}
	if len(tags) > 0 {
		json.Unmarshal(tags, &resp.Tags)
	}

	// Check if overdue
	if dueDate.Valid && t.Status != entity.TaskStatusCompleted && t.Status != entity.TaskStatusCancelled {
		resp.IsOverdue = dueDate.Time.Before(time.Now().Truncate(24 * time.Hour))
	}

	response.Success(c, resp)
}

// UpdateTask updates an existing task
func (h *Handler) UpdateTask(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid task ID")
		return
	}

	var input entity.UpdateTaskInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build update query dynamically
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.Title != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("title = $%d", argCount))
		args = append(args, *input.Title)
	}
	if input.Description != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("description = $%d", argCount))
		args = append(args, *input.Description)
	}
	if input.LeadID != nil {
		argCount++
		if *input.LeadID == "" {
			updates = append(updates, fmt.Sprintf("lead_id = $%d", argCount))
			args = append(args, nil)
		} else {
			lid, err := uuid.Parse(*input.LeadID)
			if err == nil {
				updates = append(updates, fmt.Sprintf("lead_id = $%d", argCount))
				args = append(args, lid)
			}
		}
	}
	if input.ContactID != nil {
		argCount++
		if *input.ContactID == "" {
			updates = append(updates, fmt.Sprintf("contact_id = $%d", argCount))
			args = append(args, nil)
		} else {
			cid, err := uuid.Parse(*input.ContactID)
			if err == nil {
				updates = append(updates, fmt.Sprintf("contact_id = $%d", argCount))
				args = append(args, cid)
			}
		}
	}
	if input.OpportunityID != nil {
		argCount++
		if *input.OpportunityID == "" {
			updates = append(updates, fmt.Sprintf("opportunity_id = $%d", argCount))
			args = append(args, nil)
		} else {
			oid, err := uuid.Parse(*input.OpportunityID)
			if err == nil {
				updates = append(updates, fmt.Sprintf("opportunity_id = $%d", argCount))
				args = append(args, oid)
			}
		}
	}
	if input.TaskType != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("task_type = $%d", argCount))
		args = append(args, *input.TaskType)
	}
	if input.Priority != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("priority = $%d", argCount))
		args = append(args, *input.Priority)
	}
	if input.Status != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *input.Status)

		// If completed, set completed_at
		if *input.Status == "completed" {
			argCount++
			updates = append(updates, fmt.Sprintf("completed_at = $%d", argCount))
			args = append(args, time.Now())
		}
	}
	if input.DueDate != nil {
		argCount++
		if *input.DueDate == "" {
			updates = append(updates, fmt.Sprintf("due_date = $%d", argCount))
			args = append(args, nil)
		} else {
			if t, err := time.Parse("2006-01-02", *input.DueDate); err == nil {
				updates = append(updates, fmt.Sprintf("due_date = $%d", argCount))
				args = append(args, t)
			}
		}
	}
	if input.DueTime != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("due_time = $%d", argCount))
		args = append(args, *input.DueTime)
	}
	if input.AssignedTo != nil {
		argCount++
		if *input.AssignedTo == "" {
			updates = append(updates, fmt.Sprintf("assigned_to = $%d", argCount))
			args = append(args, nil)
		} else {
			aid, err := uuid.Parse(*input.AssignedTo)
			if err == nil {
				updates = append(updates, fmt.Sprintf("assigned_to = $%d", argCount))
				args = append(args, aid)
			}
		}
	}
	if input.ProgressPercent != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("progress_percent = $%d", argCount))
		args = append(args, *input.ProgressPercent)
	}
	if input.EstimatedHours != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("estimated_hours = $%d", argCount))
		args = append(args, *input.EstimatedHours)
	}
	if input.ActualHours != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("actual_hours = $%d", argCount))
		args = append(args, *input.ActualHours)
	}
	if input.IsRecurring != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("is_recurring = $%d", argCount))
		args = append(args, *input.IsRecurring)
	}
	if input.RecurrencePattern != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("recurrence_pattern = $%d", argCount))
		args = append(args, *input.RecurrencePattern)
	}
	if input.RecurrenceEndDate != nil {
		argCount++
		if *input.RecurrenceEndDate == "" {
			updates = append(updates, fmt.Sprintf("recurrence_end_date = $%d", argCount))
			args = append(args, nil)
		} else {
			if t, err := time.Parse("2006-01-02", *input.RecurrenceEndDate); err == nil {
				updates = append(updates, fmt.Sprintf("recurrence_end_date = $%d", argCount))
				args = append(args, t)
			}
		}
	}
	if input.Tags != nil {
		argCount++
		tagsJSON, _ := json.Marshal(input.Tags)
		updates = append(updates, fmt.Sprintf("tags = $%d", argCount))
		args = append(args, tagsJSON)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	// Add updated_at
	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	// Add WHERE conditions
	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(
		"UPDATE crm_tasks SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		strings.Join(updates, ", "), argCount-1, argCount,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update task", "error", err)
		response.InternalError(c, "Failed to update task")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Task")
		return
	}

	response.Success(c, gin.H{"message": "Task updated successfully"})
}

// DeleteTask soft deletes a task
func (h *Handler) DeleteTask(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid task ID")
		return
	}

	query := `
		UPDATE crm_tasks
		SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`

	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete task", "error", err)
		response.InternalError(c, "Failed to delete task")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Task")
		return
	}

	response.Success(c, gin.H{"message": "Task deleted successfully"})
}

// CompleteTask marks a task as completed
func (h *Handler) CompleteTask(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid task ID")
		return
	}

	now := time.Now()
	query := `
		UPDATE crm_tasks
		SET status = 'completed', completed_at = $1, progress_percent = 100, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`

	result, err := h.db.Exec(query, now, id, tenantID)
	if err != nil {
		h.log.Error("Failed to complete task", "error", err)
		response.InternalError(c, "Failed to complete task")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Task")
		return
	}

	response.Success(c, gin.H{"message": "Task completed successfully"})
}

// GetTaskStats returns task statistics
func (h *Handler) GetTaskStats(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	query := `
		SELECT
			COUNT(*) as total_tasks,
			COUNT(*) FILTER (WHERE status = 'pending') as pending_tasks,
			COUNT(*) FILTER (WHERE status = 'in_progress') as in_progress_tasks,
			COUNT(*) FILTER (WHERE status = 'completed') as completed_tasks,
			COUNT(*) FILTER (WHERE due_date < CURRENT_DATE AND status NOT IN ('completed', 'cancelled')) as overdue_tasks,
			COUNT(*) FILTER (WHERE due_date = CURRENT_DATE AND status NOT IN ('completed', 'cancelled')) as due_today,
			COUNT(*) FILTER (WHERE due_date >= CURRENT_DATE AND due_date <= CURRENT_DATE + INTERVAL '7 days' AND status NOT IN ('completed', 'cancelled')) as due_this_week,
			COALESCE(
				COUNT(*) FILTER (WHERE status = 'completed')::float /
				NULLIF(COUNT(*) FILTER (WHERE status IN ('completed', 'cancelled', 'pending', 'in_progress')), 0) * 100,
				0
			) as completion_rate
		FROM crm_tasks
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`

	var stats entity.TaskStats
	err := h.db.QueryRow(query, tenantID).Scan(
		&stats.TotalTasks,
		&stats.PendingTasks,
		&stats.InProgressTasks,
		&stats.CompletedTasks,
		&stats.OverdueTasks,
		&stats.DueToday,
		&stats.DueThisWeek,
		&stats.CompletionRate,
	)

	if err != nil {
		h.log.Error("Failed to get task stats", "error", err)
		response.InternalError(c, "Failed to get task statistics")
		return
	}

	// Get by priority
	priorityQuery := `
		SELECT priority, COUNT(*)
		FROM crm_tasks
		WHERE tenant_id = $1 AND deleted_at IS NULL AND status NOT IN ('completed', 'cancelled')
		GROUP BY priority
	`
	priorityRows, err := h.db.Query(priorityQuery, tenantID)
	if err == nil {
		defer priorityRows.Close()
		stats.ByPriority = make(map[string]int)
		for priorityRows.Next() {
			var priority string
			var count int
			if priorityRows.Scan(&priority, &count) == nil {
				stats.ByPriority[priority] = count
			}
		}
	}

	// Get by type
	typeQuery := `
		SELECT task_type, COUNT(*)
		FROM crm_tasks
		WHERE tenant_id = $1 AND deleted_at IS NULL
		GROUP BY task_type
	`
	typeRows, err := h.db.Query(typeQuery, tenantID)
	if err == nil {
		defer typeRows.Close()
		stats.ByType = make(map[string]int)
		for typeRows.Next() {
			var taskType string
			var count int
			if typeRows.Scan(&taskType, &count) == nil {
				stats.ByType[taskType] = count
			}
		}
	}

	response.Success(c, stats)
}

// GetTasksKanban returns tasks organized by status for kanban view
func (h *Handler) GetTasksKanban(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	assignedTo := c.Query("assigned_to")

	// Define columns
	columns := []entity.TaskKanbanColumn{
		{Status: "pending", Name: "Pending", Tasks: make([]entity.TaskResponse, 0)},
		{Status: "in_progress", Name: "In Progress", Tasks: make([]entity.TaskResponse, 0)},
		{Status: "completed", Name: "Completed", Tasks: make([]entity.TaskResponse, 0)},
	}

	for i := range columns {
		query := `
			SELECT t.id, t.title, t.description, t.task_type, t.priority,
				   t.status, t.due_date, t.assigned_to, t.progress_percent,
				   t.created_at, t.updated_at,
				   u.first_name || ' ' || u.last_name as assigned_to_name
			FROM crm_tasks t
			LEFT JOIN users u ON t.assigned_to = u.id
			WHERE t.tenant_id = $1 AND t.status = $2 AND t.deleted_at IS NULL
		`
		args := []interface{}{tenantID, columns[i].Status}

		if assignedTo != "" {
			query += " AND t.assigned_to = $3"
			args = append(args, assignedTo)
		}

		query += " ORDER BY t.priority DESC, t.due_date ASC NULLS LAST LIMIT 50"

		rows, err := h.db.Query(query, args...)
		if err != nil {
			h.log.Error("Failed to get kanban tasks", "error", err)
			continue
		}
		defer rows.Close()

		for rows.Next() {
			var t entity.TaskResponse
			var description sql.NullString
			var dueDate sql.NullTime
			var assignedTo, assignedToName sql.NullString

			err := rows.Scan(
				&t.ID, &t.Title, &description, &t.TaskType, &t.Priority,
				&t.Status, &dueDate, &assignedTo, &t.ProgressPercent,
				&t.CreatedAt, &t.UpdatedAt, &assignedToName,
			)
			if err != nil {
				continue
			}

			if description.Valid {
				t.Description = &description.String
			}
			if dueDate.Valid {
				t.DueDate = &dueDate.Time
				if t.Status != entity.TaskStatusCompleted && dueDate.Time.Before(time.Now().Truncate(24*time.Hour)) {
					t.IsOverdue = true
				}
			}
			if assignedTo.Valid {
				aid, _ := uuid.Parse(assignedTo.String)
				t.AssignedTo = &aid
			}
			if assignedToName.Valid {
				t.AssignedToName = &assignedToName.String
			}

			columns[i].Tasks = append(columns[i].Tasks, t)
			columns[i].Count++
		}
	}

	response.Success(c, columns)
}
