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

// ListProjects returns paginated list of projects
func (h *Handler) ListProjects(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	// Pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	// Build query with filters
	baseQuery := `
		SELECT id, tenant_id, project_code, project_name, description, client_id, client_name,
			   manager_id, manager_name, start_date, end_date, budget, spent,
			   billing_type, hourly_rate, currency, priority, status, progress,
			   notes, created_by, created_at, updated_at
		FROM projects
		WHERE tenant_id = $1 AND deleted_at IS NULL`
	countQuery := `SELECT COUNT(*) FROM projects WHERE tenant_id = $1 AND deleted_at IS NULL`
	args := []interface{}{tenantID}
	argCount := 1

	// Filter by status
	if status := c.Query("status"); status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND status = $%d", argCount)
		args = append(args, status)
	}

	// Filter by priority
	if priority := c.Query("priority"); priority != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND priority = $%d", argCount)
		countQuery += fmt.Sprintf(" AND priority = $%d", argCount)
		args = append(args, priority)
	}

	// Filter by client
	if clientID := c.Query("client_id"); clientID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND client_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND client_id = $%d", argCount)
		args = append(args, clientID)
	}

	// Search
	if search := c.Query("search"); search != "" {
		argCount++
		searchPattern := "%" + strings.ToLower(search) + "%"
		baseQuery += fmt.Sprintf(" AND (LOWER(project_name) LIKE $%d OR LOWER(project_code) LIKE $%d)", argCount, argCount)
		countQuery += fmt.Sprintf(" AND (LOWER(project_name) LIKE $%d OR LOWER(project_code) LIKE $%d)", argCount, argCount)
		args = append(args, searchPattern)
	}

	// Get total count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		response.InternalError(c, "Failed to count projects")
		return
	}

	// Add sorting and pagination
	baseQuery += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", argCount+1, argCount+2)
	args = append(args, pageSize, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		response.InternalError(c, "Failed to fetch projects")
		return
	}
	defer rows.Close()

	var projects []*entity.ProjectResponse
	for rows.Next() {
		var p entity.Project
		var clientID, managerID, createdBy sql.NullString
		var description, clientName, managerName, notes sql.NullString
		var endDate, updatedAt sql.NullTime

		err := rows.Scan(
			&p.ID, &p.TenantID, &p.ProjectCode, &p.ProjectName, &description,
			&clientID, &clientName, &managerID, &managerName,
			&p.StartDate, &endDate, &p.Budget, &p.Spent,
			&p.BillingType, &p.HourlyRate, &p.Currency, &p.Priority, &p.Status, &p.Progress,
			&notes, &createdBy, &p.CreatedAt, &updatedAt,
		)
		if err != nil {
			continue
		}

		if description.Valid {
			p.Description = &description.String
		}
		if clientID.Valid {
			cid, _ := uuid.Parse(clientID.String)
			p.ClientID = &cid
		}
		if clientName.Valid {
			p.ClientName = &clientName.String
		}
		if managerID.Valid {
			mid, _ := uuid.Parse(managerID.String)
			p.ManagerID = &mid
		}
		if managerName.Valid {
			p.ManagerName = &managerName.String
		}
		if endDate.Valid {
			p.EndDate = &endDate.Time
		}
		if notes.Valid {
			p.Notes = &notes.String
		}
		if updatedAt.Valid {
			p.UpdatedAt = updatedAt.Time
		}

		projects = append(projects, p.ToResponse())
	}

	response.Paginated(c, projects, page, pageSize, total)
}

// CreateProject creates a new project
func (h *Handler) CreateProject(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateProjectInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Generate project code if not provided
	if input.ProjectCode == "" {
		input.ProjectCode = "PRJ-" + time.Now().Format("20060102") + "-" + uuid.New().String()[:4]
	}

	projectID := uuid.New()
	now := time.Now()

	// Parse start date
	startDate, err := time.Parse("2006-01-02", input.StartDate)
	if err != nil {
		response.BadRequest(c, "Invalid start_date format, expected YYYY-MM-DD")
		return
	}

	// Parse end date if provided
	var endDate *time.Time
	if input.EndDate != "" {
		ed, err := time.Parse("2006-01-02", input.EndDate)
		if err != nil {
			response.BadRequest(c, "Invalid end_date format, expected YYYY-MM-DD")
			return
		}
		endDate = &ed
	}

	// Default values
	currency := input.Currency
	if currency == "" {
		currency = "UZS"
	}
	billingType := input.BillingType
	if billingType == "" {
		billingType = "fixed"
	}
	priority := input.Priority
	if priority == "" {
		priority = "medium"
	}

	query := `
		INSERT INTO projects (
			id, tenant_id, project_code, project_name, description, client_id, client_name,
			manager_id, manager_name, start_date, end_date, budget, spent,
			billing_type, hourly_rate, currency, priority, status, progress,
			notes, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)`

	var clientID, managerID *uuid.UUID
	if input.ClientID != "" {
		cid, _ := uuid.Parse(input.ClientID)
		clientID = &cid
	}
	if input.ManagerID != "" {
		mid, _ := uuid.Parse(input.ManagerID)
		managerID = &mid
	}

	var clientName, managerName, description, notes *string
	if input.ClientName != "" {
		clientName = &input.ClientName
	}
	if input.ManagerName != "" {
		managerName = &input.ManagerName
	}
	if input.Description != "" {
		description = &input.Description
	}
	if input.Notes != "" {
		notes = &input.Notes
	}

	var createdBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
	}

	_, err = h.db.Exec(query,
		projectID, tenantID, input.ProjectCode, input.ProjectName, description,
		clientID, clientName, managerID, managerName,
		startDate, endDate, input.Budget, 0.0,
		billingType, input.HourlyRate, currency, priority, "planning", 0,
		notes, createdBy, now, now,
	)
	if err != nil {
		response.InternalError(c, "Failed to create project: "+err.Error())
		return
	}

	project := &entity.Project{
		ID:          projectID,
		TenantID:    tenantID,
		ProjectCode: input.ProjectCode,
		ProjectName: input.ProjectName,
		Description: description,
		ClientID:    clientID,
		ClientName:  clientName,
		ManagerID:   managerID,
		ManagerName: managerName,
		StartDate:   startDate,
		EndDate:     endDate,
		Budget:      input.Budget,
		Spent:       0,
		BillingType: billingType,
		HourlyRate:  input.HourlyRate,
		Currency:    currency,
		Priority:    priority,
		Status:      "planning",
		Progress:    0,
		Notes:       notes,
		CreatedBy:   createdBy,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	response.Created(c, project.ToResponse())
}

// GetProject returns a single project by ID
func (h *Handler) GetProject(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	query := `
		SELECT id, tenant_id, project_code, project_name, description, client_id, client_name,
			   manager_id, manager_name, start_date, end_date, budget, spent,
			   billing_type, hourly_rate, currency, priority, status, progress,
			   notes, created_by, created_at, updated_at
		FROM projects
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`

	var p entity.Project
	var clientID, managerID, createdBy sql.NullString
	var description, clientName, managerName, notes sql.NullString
	var endDate, updatedAt sql.NullTime

	err = h.db.QueryRow(query, projectID, tenantID).Scan(
		&p.ID, &p.TenantID, &p.ProjectCode, &p.ProjectName, &description,
		&clientID, &clientName, &managerID, &managerName,
		&p.StartDate, &endDate, &p.Budget, &p.Spent,
		&p.BillingType, &p.HourlyRate, &p.Currency, &p.Priority, &p.Status, &p.Progress,
		&notes, &createdBy, &p.CreatedAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Project")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to fetch project")
		return
	}

	if description.Valid {
		p.Description = &description.String
	}
	if clientID.Valid {
		cid, _ := uuid.Parse(clientID.String)
		p.ClientID = &cid
	}
	if clientName.Valid {
		p.ClientName = &clientName.String
	}
	if managerID.Valid {
		mid, _ := uuid.Parse(managerID.String)
		p.ManagerID = &mid
	}
	if managerName.Valid {
		p.ManagerName = &managerName.String
	}
	if endDate.Valid {
		p.EndDate = &endDate.Time
	}
	if notes.Valid {
		p.Notes = &notes.String
	}
	if updatedAt.Valid {
		p.UpdatedAt = updatedAt.Time
	}

	response.Success(c, p.ToResponse())
}

// UpdateProject updates an existing project
func (h *Handler) UpdateProject(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	var input entity.UpdateProjectInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Check if project exists
	var exists bool
	err = h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM projects WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)", projectID, tenantID).Scan(&exists)
	if err != nil || !exists {
		response.NotFound(c, "Project")
		return
	}

	// Build dynamic update query
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.ProjectName != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("project_name = $%d", argCount))
		args = append(args, *input.ProjectName)
	}
	if input.Description != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("description = $%d", argCount))
		args = append(args, *input.Description)
	}
	if input.ClientID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("client_id = $%d", argCount))
		if *input.ClientID != "" {
			cid, _ := uuid.Parse(*input.ClientID)
			args = append(args, cid)
		} else {
			args = append(args, nil)
		}
	}
	if input.ClientName != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("client_name = $%d", argCount))
		args = append(args, *input.ClientName)
	}
	if input.ManagerID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("manager_id = $%d", argCount))
		if *input.ManagerID != "" {
			mid, _ := uuid.Parse(*input.ManagerID)
			args = append(args, mid)
		} else {
			args = append(args, nil)
		}
	}
	if input.ManagerName != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("manager_name = $%d", argCount))
		args = append(args, *input.ManagerName)
	}
	if input.StartDate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("start_date = $%d", argCount))
		startDate, _ := time.Parse("2006-01-02", *input.StartDate)
		args = append(args, startDate)
	}
	if input.EndDate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("end_date = $%d", argCount))
		if *input.EndDate != "" {
			endDate, _ := time.Parse("2006-01-02", *input.EndDate)
			args = append(args, endDate)
		} else {
			args = append(args, nil)
		}
	}
	if input.Budget != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("budget = $%d", argCount))
		args = append(args, *input.Budget)
	}
	if input.Spent != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("spent = $%d", argCount))
		args = append(args, *input.Spent)
	}
	if input.BillingType != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("billing_type = $%d", argCount))
		args = append(args, *input.BillingType)
	}
	if input.HourlyRate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("hourly_rate = $%d", argCount))
		args = append(args, *input.HourlyRate)
	}
	if input.Currency != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("currency = $%d", argCount))
		args = append(args, *input.Currency)
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
	}
	if input.Progress != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("progress = $%d", argCount))
		args = append(args, *input.Progress)
	}
	if input.Notes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, *input.Notes)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	// Add updated_at
	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	// Add WHERE clause params
	argCount++
	args = append(args, projectID)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf("UPDATE projects SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		strings.Join(updates, ", "), argCount-1, argCount)

	_, err = h.db.Exec(query, args...)
	if err != nil {
		response.InternalError(c, "Failed to update project: "+err.Error())
		return
	}

	// Fetch and return updated project
	h.GetProject(c)
}

// DeleteProject soft deletes a project
func (h *Handler) DeleteProject(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	result, err := h.db.Exec(
		"UPDATE projects SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL",
		time.Now(), projectID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to delete project")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Project")
		return
	}

	response.NoContent(c)
}

// ListProjectTasks returns tasks for a project
func (h *Handler) ListProjectTasks(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	query := `
		SELECT id, tenant_id, project_id, parent_id, task_number, title, description,
			   assignee_id, assignee_name, priority, status, due_date,
			   estimated_hours, actual_hours, created_by, created_at, updated_at
		FROM project_tasks
		WHERE project_id = $1 AND tenant_id = $2 AND deleted_at IS NULL
		ORDER BY created_at DESC`

	rows, err := h.db.Query(query, projectID, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to fetch tasks")
		return
	}
	defer rows.Close()

	var tasks []*entity.ProjectTaskResponse
	for rows.Next() {
		var t entity.ProjectTask
		var parentID, assigneeID, createdBy sql.NullString
		var description, assigneeName sql.NullString
		var dueDate, updatedAt sql.NullTime

		err := rows.Scan(
			&t.ID, &t.TenantID, &t.ProjectID, &parentID, &t.TaskNumber, &t.Title, &description,
			&assigneeID, &assigneeName, &t.Priority, &t.Status, &dueDate,
			&t.EstimatedHours, &t.ActualHours, &createdBy, &t.CreatedAt, &updatedAt,
		)
		if err != nil {
			continue
		}

		if description.Valid {
			t.Description = &description.String
		}
		if parentID.Valid {
			pid, _ := uuid.Parse(parentID.String)
			t.ParentID = &pid
		}
		if assigneeID.Valid {
			aid, _ := uuid.Parse(assigneeID.String)
			t.AssigneeID = &aid
		}
		if assigneeName.Valid {
			t.AssigneeName = &assigneeName.String
		}
		if dueDate.Valid {
			t.DueDate = &dueDate.Time
		}
		if updatedAt.Valid {
			t.UpdatedAt = updatedAt.Time
		}

		tasks = append(tasks, t.ToResponse())
	}

	response.Success(c, tasks)
}

// CreateProjectTask creates a new task for a project
func (h *Handler) CreateProjectTask(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateProjectTaskInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	taskID := uuid.New()
	now := time.Now()

	// Generate task number if not provided
	taskNumber := input.TaskNumber
	if taskNumber == "" {
		taskNumber = "TASK-" + uuid.New().String()[:8]
	}

	// Parse due date if provided
	var dueDate *time.Time
	if input.DueDate != "" {
		dd, err := time.Parse("2006-01-02", input.DueDate)
		if err != nil {
			response.BadRequest(c, "Invalid due_date format, expected YYYY-MM-DD")
			return
		}
		dueDate = &dd
	}

	// Default priority
	priority := input.Priority
	if priority == "" {
		priority = "medium"
	}

	query := `
		INSERT INTO project_tasks (
			id, tenant_id, project_id, task_number, title, description,
			assignee_id, assignee_name, priority, status, due_date,
			estimated_hours, actual_hours, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`

	var assigneeID *uuid.UUID
	if input.AssigneeID != "" {
		aid, _ := uuid.Parse(input.AssigneeID)
		assigneeID = &aid
	}

	var description, assigneeName *string
	if input.Description != "" {
		description = &input.Description
	}
	if input.AssigneeName != "" {
		assigneeName = &input.AssigneeName
	}

	var createdBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
	}

	_, err = h.db.Exec(query,
		taskID, tenantID, projectID, taskNumber, input.Title, description,
		assigneeID, assigneeName, priority, "todo", dueDate,
		input.EstimatedHours, 0.0, createdBy, now, now,
	)
	if err != nil {
		response.InternalError(c, "Failed to create task: "+err.Error())
		return
	}

	task := &entity.ProjectTask{
		ID:             taskID,
		TenantID:       tenantID,
		ProjectID:      projectID,
		TaskNumber:     taskNumber,
		Title:          input.Title,
		Description:    description,
		AssigneeID:     assigneeID,
		AssigneeName:   assigneeName,
		Priority:       priority,
		Status:         "todo",
		DueDate:        dueDate,
		EstimatedHours: input.EstimatedHours,
		ActualHours:    0,
		CreatedBy:      createdBy,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	response.Created(c, task.ToResponse())
}

// UpdateProjectTask updates an existing task
func (h *Handler) UpdateProjectTask(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	taskID, err := uuid.Parse(c.Param("taskId"))
	if err != nil {
		response.BadRequest(c, "Invalid task ID")
		return
	}

	var input entity.UpdateProjectTaskInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Build dynamic update query
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
	if input.AssigneeID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("assignee_id = $%d", argCount))
		if *input.AssigneeID != "" {
			aid, _ := uuid.Parse(*input.AssigneeID)
			args = append(args, aid)
		} else {
			args = append(args, nil)
		}
	}
	if input.AssigneeName != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("assignee_name = $%d", argCount))
		args = append(args, *input.AssigneeName)
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
	}
	if input.DueDate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("due_date = $%d", argCount))
		if *input.DueDate != "" {
			dd, _ := time.Parse("2006-01-02", *input.DueDate)
			args = append(args, dd)
		} else {
			args = append(args, nil)
		}
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

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	// Add updated_at
	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	// Add WHERE clause params
	argCount++
	args = append(args, taskID)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf("UPDATE project_tasks SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		strings.Join(updates, ", "), argCount-1, argCount)

	result, err := h.db.Exec(query, args...)
	if err != nil {
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

// DeleteProjectTask soft deletes a task
func (h *Handler) DeleteProjectTask(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	taskID, err := uuid.Parse(c.Param("taskId"))
	if err != nil {
		response.BadRequest(c, "Invalid task ID")
		return
	}

	result, err := h.db.Exec(
		"UPDATE project_tasks SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL",
		time.Now(), taskID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to delete task")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Task")
		return
	}

	response.NoContent(c)
}

// ListProjectMilestones returns milestones for a project
func (h *Handler) ListProjectMilestones(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	query := `
		SELECT id, tenant_id, project_id, title, description, due_date, status, completed_date, created_at, updated_at
		FROM project_milestones
		WHERE project_id = $1 AND tenant_id = $2
		ORDER BY due_date ASC`

	rows, err := h.db.Query(query, projectID, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to fetch milestones")
		return
	}
	defer rows.Close()

	var milestones []*entity.MilestoneResponse
	for rows.Next() {
		var m entity.ProjectMilestone
		var description sql.NullString
		var completedDate, updatedAt sql.NullTime

		err := rows.Scan(
			&m.ID, &m.TenantID, &m.ProjectID, &m.Title, &description,
			&m.DueDate, &m.Status, &completedDate, &m.CreatedAt, &updatedAt,
		)
		if err != nil {
			continue
		}

		if description.Valid {
			m.Description = &description.String
		}
		if completedDate.Valid {
			m.CompletedDate = &completedDate.Time
		}
		if updatedAt.Valid {
			m.UpdatedAt = updatedAt.Time
		}

		milestones = append(milestones, m.ToResponse())
	}

	response.Success(c, milestones)
}

// CreateProjectMilestone creates a new milestone for a project
func (h *Handler) CreateProjectMilestone(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	var input entity.CreateMilestoneInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	milestoneID := uuid.New()
	now := time.Now()

	// Parse due date
	dueDate, err := time.Parse("2006-01-02", input.DueDate)
	if err != nil {
		response.BadRequest(c, "Invalid due_date format, expected YYYY-MM-DD")
		return
	}

	query := `
		INSERT INTO project_milestones (id, tenant_id, project_id, title, description, due_date, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	var description *string
	if input.Description != "" {
		description = &input.Description
	}

	_, err = h.db.Exec(query,
		milestoneID, tenantID, projectID, input.Title, description, dueDate, "pending", now, now,
	)
	if err != nil {
		response.InternalError(c, "Failed to create milestone: "+err.Error())
		return
	}

	milestone := &entity.ProjectMilestone{
		ID:          milestoneID,
		TenantID:    tenantID,
		ProjectID:   projectID,
		Title:       input.Title,
		Description: description,
		DueDate:     dueDate,
		Status:      "pending",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	response.Created(c, milestone.ToResponse())
}

// ListTimeEntries returns time entries for a project
func (h *Handler) ListTimeEntries(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	query := `
		SELECT id, tenant_id, project_id, task_id, employee_id, employee_name,
			   entry_date, hours, description, billable, hourly_rate, amount, status,
			   approved_by, approved_at, created_at, updated_at
		FROM time_entries
		WHERE project_id = $1 AND tenant_id = $2 AND deleted_at IS NULL
		ORDER BY entry_date DESC`

	rows, err := h.db.Query(query, projectID, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to fetch time entries")
		return
	}
	defer rows.Close()

	var entries []*entity.TimeEntryResponse
	for rows.Next() {
		var e entity.TimeEntry
		var taskID, approvedBy sql.NullString
		var description sql.NullString
		var hourlyRate, amount sql.NullFloat64
		var approvedAt, updatedAt sql.NullTime

		err := rows.Scan(
			&e.ID, &e.TenantID, &e.ProjectID, &taskID, &e.EmployeeID, &e.EmployeeName,
			&e.EntryDate, &e.Hours, &description, &e.Billable, &hourlyRate, &amount, &e.Status,
			&approvedBy, &approvedAt, &e.CreatedAt, &updatedAt,
		)
		if err != nil {
			continue
		}

		if taskID.Valid {
			tid, _ := uuid.Parse(taskID.String)
			e.TaskID = &tid
		}
		if description.Valid {
			e.Description = &description.String
		}
		if hourlyRate.Valid {
			e.HourlyRate = &hourlyRate.Float64
		}
		if amount.Valid {
			e.Amount = &amount.Float64
		}
		if approvedBy.Valid {
			aid, _ := uuid.Parse(approvedBy.String)
			e.ApprovedBy = &aid
		}
		if approvedAt.Valid {
			e.ApprovedAt = &approvedAt.Time
		}
		if updatedAt.Valid {
			e.UpdatedAt = updatedAt.Time
		}

		entries = append(entries, e.ToResponse())
	}

	response.Success(c, entries)
}

// CreateTimeEntry creates a new time entry for a project
func (h *Handler) CreateTimeEntry(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	var input entity.CreateTimeEntryInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	entryID := uuid.New()
	now := time.Now()

	// Parse employee ID
	employeeID, err := uuid.Parse(input.EmployeeID)
	if err != nil {
		response.BadRequest(c, "Invalid employee_id")
		return
	}

	// Parse entry date
	entryDate, err := time.Parse("2006-01-02", input.EntryDate)
	if err != nil {
		response.BadRequest(c, "Invalid date format, expected YYYY-MM-DD")
		return
	}

	query := `
		INSERT INTO time_entries (
			id, tenant_id, project_id, task_id, employee_id, employee_name,
			entry_date, hours, description, billable, hourly_rate, amount, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`

	var taskID *uuid.UUID
	if input.TaskID != "" {
		tid, _ := uuid.Parse(input.TaskID)
		taskID = &tid
	}

	var description *string
	if input.Description != "" {
		description = &input.Description
	}

	var hourlyRate *float64
	var amount *float64
	if input.HourlyRate > 0 {
		hourlyRate = &input.HourlyRate
		amt := input.Hours * input.HourlyRate
		amount = &amt
	}

	_, err = h.db.Exec(query,
		entryID, tenantID, projectID, taskID, employeeID, input.EmployeeName,
		entryDate, input.Hours, description, input.Billable, hourlyRate, amount, "pending", now, now,
	)
	if err != nil {
		response.InternalError(c, "Failed to create time entry: "+err.Error())
		return
	}

	// Update actual_hours on task if linked
	if taskID != nil {
		h.db.Exec(
			"UPDATE project_tasks SET actual_hours = actual_hours + $1, updated_at = $2 WHERE id = $3",
			input.Hours, now, taskID,
		)
	}

	// Update spent on project based on hourly rate
	if input.Billable && hourlyRate != nil {
		h.db.Exec(
			"UPDATE projects SET spent = spent + $1, updated_at = $2 WHERE id = $3",
			*amount, now, projectID,
		)
	}

	entry := &entity.TimeEntry{
		ID:           entryID,
		TenantID:     tenantID,
		ProjectID:    projectID,
		TaskID:       taskID,
		EmployeeID:   employeeID,
		EmployeeName: input.EmployeeName,
		EntryDate:    entryDate,
		Hours:        input.Hours,
		Description:  description,
		Billable:     input.Billable,
		HourlyRate:   hourlyRate,
		Amount:       amount,
		Status:       "pending",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	response.Created(c, entry.ToResponse())
}
