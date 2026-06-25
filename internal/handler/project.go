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

	// Build query with filters - include calculated total_hours from time_entries
	baseQuery := `
		SELECT p.id, p.tenant_id, p.project_code, p.project_name, p.description, p.client_id, p.client_name,
			   p.manager_id, p.manager_name, p.start_date, p.end_date, p.budget, p.spent,
			   p.billing_type, p.hourly_rate, p.currency, p.priority, p.status, p.progress,
			   p.notes, p.created_by, p.created_at, p.updated_at,
			   COALESCE((SELECT SUM(hours) FROM time_entries WHERE project_id = p.id AND deleted_at IS NULL), 0) as total_hours
		FROM projects p
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL`
	countQuery := `SELECT COUNT(*) FROM projects p WHERE p.tenant_id = $1 AND p.deleted_at IS NULL`
	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND p.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND p.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	// Filter by status
	if status := c.Query("status"); status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND p.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND p.status = $%d", argCount)
		args = append(args, status)
	}

	// Filter by priority
	if priority := c.Query("priority"); priority != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND p.priority = $%d", argCount)
		countQuery += fmt.Sprintf(" AND p.priority = $%d", argCount)
		args = append(args, priority)
	}

	// Filter by client
	if clientID := c.Query("client_id"); clientID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND p.client_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND p.client_id = $%d", argCount)
		args = append(args, clientID)
	}

	// Search
	if search := c.Query("search"); search != "" {
		argCount++
		searchPattern := "%" + strings.ToLower(search) + "%"
		baseQuery += fmt.Sprintf(" AND (LOWER(p.project_name) LIKE $%d OR LOWER(p.project_code) LIKE $%d)", argCount, argCount)
		countQuery += fmt.Sprintf(" AND (LOWER(p.project_name) LIKE $%d OR LOWER(p.project_code) LIKE $%d)", argCount, argCount)
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
			&p.TotalHours,
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

// ListProjectsByOrganization returns projects for a specific organization (used for intercompany)
func (h *Handler) ListProjectsByOrganization(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	orgID := c.Query("organization_id")
	if orgID == "" {
		response.BadRequest(c, "organization_id is required")
		return
	}

	parsedOrgID, err := uuid.Parse(orgID)
	if err != nil {
		response.BadRequest(c, "Invalid organization_id")
		return
	}

	// Verify that this organization belongs to the same tenant
	var exists bool
	err = h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM organizations WHERE id = $1 AND tenant_id = $2)`, parsedOrgID, tenantID).Scan(&exists)
	if err != nil || !exists {
		response.NotFound(c, "Organization")
		return
	}

	// Fetch construction projects: first try matching org, then fall back to all tenant projects
	query := `
		SELECT cp.id, cp.code, cp.name, cp.status
		FROM construction_projects cp
		WHERE cp.tenant_id = $1 AND cp.deleted_at IS NULL
		  AND cp.status IN ('draft', 'planning', 'active', 'in_progress')
		  AND (cp.organization_id = $2 OR cp.organization_id IS NULL OR cp.organization_id = '00000000-0000-0000-0000-000000000000')
		ORDER BY cp.name ASC`

	rows, err := h.db.Query(query, tenantID, parsedOrgID)
	if err != nil {
		response.InternalError(c, "Failed to fetch projects")
		return
	}
	defer rows.Close()

	var projects []map[string]interface{}
	for rows.Next() {
		var id int64
		var projectCode, projectName, status string
		if err := rows.Scan(&id, &projectCode, &projectName, &status); err != nil {
			continue
		}
		projects = append(projects, map[string]interface{}{
			"id":           fmt.Sprintf("%d", id),
			"project_code": projectCode,
			"project_name": projectName,
			"status":       status,
		})
	}

	// If no projects found with org filter, fetch all construction projects in the tenant
	if len(projects) == 0 {
		fallbackQuery := `
			SELECT cp.id, cp.code, cp.name, cp.status
			FROM construction_projects cp
			WHERE cp.tenant_id = $1 AND cp.deleted_at IS NULL
			  AND cp.status IN ('draft', 'planning', 'active', 'in_progress')
			ORDER BY cp.name ASC`

		fbRows, fbErr := h.db.Query(fallbackQuery, tenantID)
		if fbErr == nil {
			defer fbRows.Close()
			for fbRows.Next() {
				var id int64
				var projectCode, projectName, status string
				if fbErr := fbRows.Scan(&id, &projectCode, &projectName, &status); fbErr != nil {
					continue
				}
				projects = append(projects, map[string]interface{}{
					"id":           fmt.Sprintf("%d", id),
					"project_code": projectCode,
					"project_name": projectName,
					"status":       status,
				})
			}
		}
	}

	if projects == nil {
		projects = []map[string]interface{}{}
	}

	response.Success(c, projects)
}

// CreateProject creates a new project
func (h *Handler) CreateProject(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	userID, _ := middleware.GetUserID(c)

	// Get organization ID from middleware header
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	var input entity.CreateProjectInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
			id, tenant_id, organization_id, project_code, project_name, description, client_id, client_name,
			manager_id, manager_name, start_date, end_date, budget, spent,
			billing_type, hourly_rate, currency, priority, status, progress,
			notes, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)`

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
		projectID, tenantID, orgIDPtr, input.ProjectCode, input.ProjectName, description,
		clientID, clientName, managerID, managerName,
		startDate, endDate, input.Budget, 0.0,
		billingType, input.HourlyRate, currency, priority, "planning", 0,
		notes, createdBy, now, now,
	)
	if err != nil {
		h.log.Error("Failed to create project", "error", err)
		response.InternalError(c, "Failed to create project")
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
		SELECT p.id, p.tenant_id, p.project_code, p.project_name, p.description, p.client_id, p.client_name,
			   p.manager_id, p.manager_name, p.start_date, p.end_date, p.budget, p.spent,
			   p.billing_type, p.hourly_rate, p.currency, p.priority, p.status, p.progress,
			   p.notes, p.created_by, p.created_at, p.updated_at,
			   COALESCE((SELECT SUM(hours) FROM time_entries WHERE project_id = p.id AND deleted_at IS NULL), 0) as total_hours
		FROM projects p
		WHERE p.id = $1 AND p.tenant_id = $2 AND p.deleted_at IS NULL`

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
		&p.TotalHours,
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
		h.log.Error("Failed to update project", "error", err)
		response.InternalError(c, "Failed to update project")
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

	paginate, page, pageSize, offset := optPagination(c)

	query := `
		SELECT id, tenant_id, project_id, parent_id, milestone_id, task_number, title, description,
			   assignee_id, assignee_name, priority, status, due_date,
			   estimated_hours, actual_hours, created_by, created_at, updated_at,
			   (SELECT COUNT(*) FROM project_task_notes ptn WHERE ptn.task_id = project_tasks.id) AS note_count,
			   (SELECT COALESCE(json_agg(json_build_object('employee_id', ta.employee_id, 'employee_name', ta.employee_name) ORDER BY ta.created_at), '[]'::json)
			    FROM project_task_assignees ta WHERE ta.task_id = project_tasks.id) AS assignees
		FROM project_tasks
		WHERE project_id = $1 AND tenant_id = $2 AND deleted_at IS NULL
		ORDER BY created_at DESC`
	args := []interface{}{projectID, tenantID}
	if paginate {
		query += " LIMIT $3 OFFSET $4"
		args = append(args, pageSize, offset)
	}

	rows, err := h.db.Query(query, args...)
	if err != nil {
		response.InternalError(c, "Failed to fetch tasks")
		return
	}
	defer rows.Close()

	var tasks []*entity.ProjectTaskResponse
	for rows.Next() {
		var t entity.ProjectTask
		var parentID, milestoneID, assigneeID, createdBy sql.NullString
		var description, assigneeName sql.NullString
		var dueDate, updatedAt sql.NullTime
		var assigneesJSON []byte

		err := rows.Scan(
			&t.ID, &t.TenantID, &t.ProjectID, &parentID, &milestoneID, &t.TaskNumber, &t.Title, &description,
			&assigneeID, &assigneeName, &t.Priority, &t.Status, &dueDate,
			&t.EstimatedHours, &t.ActualHours, &createdBy, &t.CreatedAt, &updatedAt, &t.NoteCount, &assigneesJSON,
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
		if milestoneID.Valid {
			mid, _ := uuid.Parse(milestoneID.String)
			t.MilestoneID = &mid
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

		resp := t.ToResponse()
		if len(assigneesJSON) > 0 {
			_ = json.Unmarshal(assigneesJSON, &resp.Assignees)
		}
		if resp.Assignees == nil {
			resp.Assignees = []entity.TaskAssignee{}
		}
		tasks = append(tasks, resp)
	}

	if !paginate {
		response.Success(c, tasks)
		return
	}
	var total int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM project_tasks WHERE project_id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, projectID, tenantID).Scan(&total)
	response.Paginated(c, tasks, page, pageSize, total)
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
			estimated_hours, actual_hours, created_by, created_at, updated_at, milestone_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`

	// Determine the assignee set: prefer the multi-assignee list, else the single field.
	assigneeSet := input.Assignees
	if len(assigneeSet) == 0 && input.AssigneeID != "" {
		assigneeSet = []entity.TaskAssignee{{EmployeeID: input.AssigneeID, EmployeeName: input.AssigneeName}}
	}

	var assigneeID *uuid.UUID
	var assigneeName *string
	if len(assigneeSet) > 0 {
		// Primary assignee = first in the set (kept on the task row for compat)
		if aid, err := uuid.Parse(assigneeSet[0].EmployeeID); err == nil {
			assigneeID = &aid
		}
		if assigneeSet[0].EmployeeName != "" {
			n := assigneeSet[0].EmployeeName
			assigneeName = &n
		}
	}

	var milestoneID *uuid.UUID
	if input.MilestoneID != "" {
		mid, _ := uuid.Parse(input.MilestoneID)
		milestoneID = &mid
	}

	var description *string
	if input.Description != "" {
		description = &input.Description
	}

	var createdBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
	}

	_, err = h.db.Exec(query,
		taskID, tenantID, projectID, taskNumber, input.Title, description,
		assigneeID, assigneeName, priority, "todo", dueDate,
		input.EstimatedHours, 0.0, createdBy, now, now, milestoneID,
	)
	if err != nil {
		h.log.Error("Failed to create task", "error", err)
		response.InternalError(c, "Failed to create task")
		return
	}

	// Insert assignee junction rows
	for _, a := range assigneeSet {
		eid, perr := uuid.Parse(a.EmployeeID)
		if perr != nil {
			continue
		}
		h.db.Exec(`INSERT INTO project_task_assignees (id, tenant_id, task_id, employee_id, employee_name, created_at)
			VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (task_id, employee_id) DO NOTHING`,
			uuid.New(), tenantID, taskID, eid, a.EmployeeName, now)
	}

	// Workflow: notify assignees a task was assigned to them
	if len(assigneeSet) > 0 {
		ids := make([]string, 0, len(assigneeSet))
		for _, a := range assigneeSet {
			ids = append(ids, a.EmployeeID)
		}
		h.fireProjectTaskAssigned(tenantID, projectID, taskID, input.Title, ids)
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
		MilestoneID:    milestoneID,
		Priority:       priority,
		Status:         "todo",
		DueDate:        dueDate,
		EstimatedHours: input.EstimatedHours,
		ActualHours:    0,
		CreatedBy:      createdBy,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	resp := task.ToResponse()
	resp.Assignees = assigneeSet
	if resp.Assignees == nil {
		resp.Assignees = []entity.TaskAssignee{}
	}
	response.Created(c, resp)
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Capture current status/title/project for workflow events
	var oldStatus, oldTitle string
	var wfProjectID uuid.UUID
	h.db.QueryRow(`SELECT status, title, project_id FROM project_tasks WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, taskID, tenantID).Scan(&oldStatus, &oldTitle, &wfProjectID)

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
	if input.MilestoneID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("milestone_id = $%d", argCount))
		if *input.MilestoneID != "" {
			mid, _ := uuid.Parse(*input.MilestoneID)
			args = append(args, mid)
		} else {
			args = append(args, nil)
		}
	}
	// Keep the primary assignee column in sync with the multi-assignee list
	if input.Assignees != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("assignee_id = $%d", argCount))
		if len(input.Assignees) > 0 {
			if aid, err := uuid.Parse(input.Assignees[0].EmployeeID); err == nil {
				args = append(args, aid)
			} else {
				args = append(args, nil)
			}
		} else {
			args = append(args, nil)
		}
		argCount++
		updates = append(updates, fmt.Sprintf("assignee_name = $%d", argCount))
		if len(input.Assignees) > 0 {
			args = append(args, input.Assignees[0].EmployeeName)
		} else {
			args = append(args, nil)
		}
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

	// Replace the assignee set when provided
	if input.Assignees != nil {
		h.db.Exec(`DELETE FROM project_task_assignees WHERE task_id = $1 AND tenant_id = $2`, taskID, tenantID)
		for _, a := range input.Assignees {
			if eid, perr := uuid.Parse(a.EmployeeID); perr == nil {
				h.db.Exec(`INSERT INTO project_task_assignees (id, tenant_id, task_id, employee_id, employee_name, created_at)
					VALUES ($1, $2, $3, $4, $5, NOW()) ON CONFLICT (task_id, employee_id) DO NOTHING`,
					uuid.New(), tenantID, taskID, eid, a.EmployeeName)
			}
		}
	}

	// Workflow events
	wfTitle := oldTitle
	if input.Title != nil && *input.Title != "" {
		wfTitle = *input.Title
	}
	if wfProjectID != uuid.Nil {
		// Status changed
		if input.Status != nil && *input.Status != oldStatus {
			h.fireProjectTaskStatusChanged(tenantID, wfProjectID, taskID, wfTitle, *input.Status, h.taskAssigneeEmployeeIDs(tenantID, taskID))
		}
		// (Re)assigned
		if input.Assignees != nil && len(input.Assignees) > 0 {
			ids := make([]string, 0, len(input.Assignees))
			for _, a := range input.Assignees {
				ids = append(ids, a.EmployeeID)
			}
			h.fireProjectTaskAssigned(tenantID, wfProjectID, taskID, wfTitle, ids)
		}
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

	paginate, page, pageSize, offset := optPagination(c)

	query := `
		SELECT id, tenant_id, project_id, title, description, due_date, status, completed_date, created_at, updated_at
		FROM project_milestones
		WHERE project_id = $1 AND tenant_id = $2
		ORDER BY due_date ASC`
	args := []interface{}{projectID, tenantID}
	if paginate {
		query += " LIMIT $3 OFFSET $4"
		args = append(args, pageSize, offset)
	}

	rows, err := h.db.Query(query, args...)
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

	if !paginate {
		response.Success(c, milestones)
		return
	}
	var total int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM project_milestones WHERE project_id = $1 AND tenant_id = $2`, projectID, tenantID).Scan(&total)
	response.Paginated(c, milestones, page, pageSize, total)
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
		h.log.Error("Failed to create milestone", "error", err)
		response.InternalError(c, "Failed to create milestone")
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

// UpdateProjectMilestone updates an existing milestone
func (h *Handler) UpdateProjectMilestone(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	milestoneID, err := uuid.Parse(c.Param("milestoneId"))
	if err != nil {
		response.BadRequest(c, "Invalid milestone ID")
		return
	}

	var input entity.UpdateMilestoneInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Capture current status so we only fire the "milestone completed" event on
	// an actual transition into 'completed' (not on every save of a done one).
	var oldMilestoneStatus string
	h.db.QueryRow(`SELECT status FROM project_milestones WHERE id = $1 AND tenant_id = $2`, milestoneID, tenantID).Scan(&oldMilestoneStatus)

	// Build update query
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
	if input.DueDate != nil {
		dueDate, err := time.Parse("2006-01-02", *input.DueDate)
		if err != nil {
			response.BadRequest(c, "Invalid due_date format")
			return
		}
		argCount++
		updates = append(updates, fmt.Sprintf("due_date = $%d", argCount))
		args = append(args, dueDate)
	}
	if input.Status != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *input.Status)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	// Add milestone ID and tenant ID for WHERE clause
	milestoneIDPlaceholder := argCount + 1
	tenantIDPlaceholder := argCount + 2
	args = append(args, milestoneID)
	args = append(args, tenantID)

	query := fmt.Sprintf(
		"UPDATE project_milestones SET %s WHERE id = $%d AND tenant_id = $%d",
		strings.Join(updates, ", "), milestoneIDPlaceholder, tenantIDPlaceholder,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update milestone", "error", err)
		response.InternalError(c, "Failed to update milestone")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Milestone")
		return
	}

	// Workflow event: milestone completed — only on transition into 'completed'
	if input.Status != nil && *input.Status == "completed" && oldMilestoneStatus != "completed" {
		var wfProjectID uuid.UUID
		var wfTitle string
		if err := h.db.QueryRow(`SELECT project_id, title FROM project_milestones WHERE id = $1 AND tenant_id = $2`, milestoneID, tenantID).Scan(&wfProjectID, &wfTitle); err == nil && wfProjectID != uuid.Nil {
			h.fireProjectMilestoneCompleted(tenantID, wfProjectID, milestoneID, wfTitle)
		}
	}

	response.Success(c, gin.H{"message": "Milestone updated successfully"})
}

// DeleteProjectMilestone deletes a project milestone
func (h *Handler) DeleteProjectMilestone(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	milestoneID, err := uuid.Parse(c.Param("milestoneId"))
	if err != nil {
		response.BadRequest(c, "Invalid milestone ID")
		return
	}

	result, err := h.db.Exec(
		"DELETE FROM project_milestones WHERE id = $1 AND tenant_id = $2",
		milestoneID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to delete milestone")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Milestone")
		return
	}

	response.NoContent(c)
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

	paginate, page, pageSize, offset := optPagination(c)

	query := `
		SELECT id, tenant_id, project_id, task_id, employee_id, employee_name,
			   entry_date, hours, description, billable, hourly_rate, amount, status,
			   approved_by, approved_at, created_at, updated_at
		FROM time_entries
		WHERE project_id = $1 AND tenant_id = $2 AND deleted_at IS NULL
		ORDER BY entry_date DESC`
	args := []interface{}{projectID, tenantID}
	if paginate {
		query += " LIMIT $3 OFFSET $4"
		args = append(args, pageSize, offset)
	}

	rows, err := h.db.Query(query, args...)
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

	if !paginate {
		response.Success(c, entries)
		return
	}
	var total int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM time_entries WHERE project_id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, projectID, tenantID).Scan(&total)
	response.Paginated(c, entries, page, pageSize, total)
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
		h.log.Error("Failed to create time entry", "error", err)
		response.InternalError(c, "Failed to create time entry")
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

// ListProjectExpenses returns expenses for a project
func (h *Handler) ListProjectExpenses(c *gin.Context) {
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

	paginate, page, pageSize, offset := optPagination(c)

	query := `
		SELECT pe.id, pe.tenant_id, pe.project_id, pe.expense_number, pe.category, pe.description,
			   pe.amount, pe.currency, pe.expense_date, pe.employee_id, pe.employee_name,
			   pe.vendor_id, pe.vendor_name, pe.purchase_invoice_id, pe.receipt_url, pe.billable, pe.status,
			   pe.approved_by, pe.approved_at, pe.notes, pe.created_at, pe.updated_at,
			   c.name as vendor_display_name
		FROM project_expenses pe
		LEFT JOIN contacts c ON pe.vendor_id = c.id
		WHERE pe.project_id = $1 AND pe.tenant_id = $2 AND pe.deleted_at IS NULL
		ORDER BY pe.expense_date DESC`
	args := []interface{}{projectID, tenantID}
	if paginate {
		query += " LIMIT $3 OFFSET $4"
		args = append(args, pageSize, offset)
	}

	rows, err := h.db.Query(query, args...)
	if err != nil {
		response.InternalError(c, "Failed to fetch project expenses")
		return
	}
	defer rows.Close()

	var expenses []*entity.ProjectExpenseResponse
	for rows.Next() {
		var e entity.ProjectExpense
		var employeeID, vendorID, purchaseInvoiceID, approvedBy sql.NullString
		var category, employeeName, vendorName, receiptURL, notes, vendorDisplayName sql.NullString
		var approvedAt, updatedAt sql.NullTime

		err := rows.Scan(
			&e.ID, &e.TenantID, &e.ProjectID, &e.ExpenseNumber, &category, &e.Description,
			&e.Amount, &e.Currency, &e.ExpenseDate, &employeeID, &employeeName,
			&vendorID, &vendorName, &purchaseInvoiceID, &receiptURL, &e.Billable, &e.Status,
			&approvedBy, &approvedAt, &notes, &e.CreatedAt, &updatedAt,
			&vendorDisplayName,
		)
		if err != nil {
			continue
		}

		if category.Valid {
			e.Category = &category.String
		}
		if employeeID.Valid {
			eid, _ := uuid.Parse(employeeID.String)
			e.EmployeeID = &eid
		}
		if employeeName.Valid {
			e.EmployeeName = &employeeName.String
		}
		if vendorID.Valid {
			vid, _ := uuid.Parse(vendorID.String)
			e.VendorID = &vid
		}
		// Use vendor display name from contacts if available, else fallback to stored vendor_name
		if vendorDisplayName.Valid {
			e.VendorName = &vendorDisplayName.String
		} else if vendorName.Valid {
			e.VendorName = &vendorName.String
		}
		if purchaseInvoiceID.Valid {
			piid, _ := uuid.Parse(purchaseInvoiceID.String)
			e.PurchaseInvoiceID = &piid
		}
		if receiptURL.Valid {
			e.ReceiptURL = &receiptURL.String
		}
		if notes.Valid {
			e.Notes = &notes.String
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

		expenses = append(expenses, e.ToResponse())
	}

	if !paginate {
		response.Success(c, expenses)
		return
	}
	var total int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM project_expenses pe WHERE pe.project_id = $1 AND pe.tenant_id = $2 AND pe.deleted_at IS NULL`, projectID, tenantID).Scan(&total)
	response.Paginated(c, expenses, page, pageSize, total)
}

// CreateProjectExpense creates a new expense for a project
func (h *Handler) CreateProjectExpense(c *gin.Context) {
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

	var input entity.CreateProjectExpenseInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	expenseID := uuid.New()
	now := time.Now()

	// Generate expense number
	expenseNumber := fmt.Sprintf("PEXP-%s-%s", now.Format("20060102"), uuid.New().String()[:6])

	// Parse expense date
	expenseDate, err := time.Parse("2006-01-02", input.ExpenseDate)
	if err != nil {
		response.BadRequest(c, "Invalid expense_date format, expected YYYY-MM-DD")
		return
	}

	// Default currency
	currency := input.Currency
	if currency == "" {
		currency = "UZS"
	}

	var employeeID *uuid.UUID
	if input.EmployeeID != "" {
		eid, _ := uuid.Parse(input.EmployeeID)
		employeeID = &eid
	}

	var vendorID *uuid.UUID
	if input.VendorID != "" {
		vid, _ := uuid.Parse(input.VendorID)
		vendorID = &vid
	}

	var category, employeeName, vendorName, receiptURL, notes *string
	if input.Category != "" {
		category = &input.Category
	}
	if input.EmployeeName != "" {
		employeeName = &input.EmployeeName
	}
	if input.VendorName != "" {
		vendorName = &input.VendorName
	}
	if input.ReceiptURL != "" {
		receiptURL = &input.ReceiptURL
	}
	if input.Notes != "" {
		notes = &input.Notes
	}

	var createdBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
	}

	// If vendor is provided, create a purchase invoice (payable)
	var purchaseInvoiceID *uuid.UUID
	if vendorID != nil {
		invoiceID := uuid.New()
		purchaseInvoiceID = &invoiceID
		invoiceNumber := fmt.Sprintf("BILL-%s-%s", now.Format("20060102"), uuid.New().String()[:6])

		// Due date is 30 days from expense date
		dueDate := expenseDate.AddDate(0, 0, 30)

		// Get project name for invoice notes
		var projectName string
		h.db.QueryRow("SELECT project_name FROM projects WHERE id = $1", projectID).Scan(&projectName)

		invoiceNotes := fmt.Sprintf("Project Expense: %s - %s", projectName, input.Description)

		// Create purchase invoice
		_, err = h.db.Exec(`
			INSERT INTO purchase_invoices (
				id, tenant_id, invoice_number, vendor_id, vendor_invoice_number,
				invoice_date, due_date, subtotal, discount_amount,
				tax_amount, total_amount, amount_paid, status,
				three_way_match_status, notes, created_by, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`,
			invoiceID, tenantID, invoiceNumber, vendorID, expenseNumber,
			expenseDate, dueDate, input.Amount, 0,
			0, input.Amount, 0, "draft",
			"pending", invoiceNotes, createdBy, now, now,
		)
		if err != nil {
			h.log.Error("Failed to create purchase invoice for project expense", "error", err)
			// Continue without purchase invoice - don't fail the expense creation
			purchaseInvoiceID = nil
		} else {
			h.log.Info("Purchase invoice created for project expense", "invoice_id", invoiceID, "expense_id", expenseID)
		}
	}

	query := `
		INSERT INTO project_expenses (
			id, tenant_id, project_id, expense_number, category, description,
			amount, currency, expense_date, employee_id, employee_name,
			vendor_id, vendor_name, purchase_invoice_id, receipt_url, billable, status, notes, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)`

	_, err = h.db.Exec(query,
		expenseID, tenantID, projectID, expenseNumber, category, input.Description,
		input.Amount, currency, expenseDate, employeeID, employeeName,
		vendorID, vendorName, purchaseInvoiceID, receiptURL, input.Billable, "pending", notes, createdBy, now, now,
	)
	if err != nil {
		h.log.Error("Failed to create expense", "error", err)
		response.InternalError(c, "Failed to create expense")
		return
	}

	// Update spent on project
	if input.Billable {
		h.db.Exec(
			"UPDATE projects SET spent = spent + $1, updated_at = $2 WHERE id = $3",
			input.Amount, now, projectID,
		)
	}

	expense := &entity.ProjectExpense{
		ID:                expenseID,
		TenantID:          tenantID,
		ProjectID:         projectID,
		ExpenseNumber:     expenseNumber,
		Category:          category,
		Description:       input.Description,
		Amount:            input.Amount,
		Currency:          currency,
		ExpenseDate:       expenseDate,
		EmployeeID:        employeeID,
		EmployeeName:      employeeName,
		VendorID:          vendorID,
		VendorName:        vendorName,
		PurchaseInvoiceID: purchaseInvoiceID,
		ReceiptURL:        receiptURL,
		Billable:          input.Billable,
		Status:            "pending",
		Notes:             notes,
		CreatedBy:         createdBy,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	response.Created(c, expense.ToResponse())
}

// DeleteProjectExpense soft deletes a project expense
func (h *Handler) DeleteProjectExpense(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	expenseID, err := uuid.Parse(c.Param("expenseId"))
	if err != nil {
		response.BadRequest(c, "Invalid expense ID")
		return
	}

	// Get the expense amount to deduct from project spent
	var amount float64
	var projectID uuid.UUID
	var billable bool
	err = h.db.QueryRow(
		"SELECT project_id, amount, billable FROM project_expenses WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL",
		expenseID, tenantID,
	).Scan(&projectID, &amount, &billable)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Expense")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to fetch expense")
		return
	}

	now := time.Now()
	result, err := h.db.Exec(
		"UPDATE project_expenses SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL",
		now, expenseID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to delete expense")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Expense")
		return
	}

	// Deduct from project spent if billable
	if billable {
		h.db.Exec(
			"UPDATE projects SET spent = spent - $1, updated_at = $2 WHERE id = $3",
			amount, now, projectID,
		)
	}

	response.NoContent(c)
}

// ListProjectTeamMembers returns team members for a project
func (h *Handler) ListProjectTeamMembers(c *gin.Context) {
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
		SELECT id, tenant_id, project_id, employee_id, employee_name,
			   role, allocation_percent, start_date, end_date, created_at, updated_at
		FROM project_team_members
		WHERE project_id = $1 AND tenant_id = $2
		ORDER BY created_at ASC`

	rows, err := h.db.Query(query, projectID, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to fetch team members")
		return
	}
	defer rows.Close()

	var members []*entity.ProjectTeamMemberResponse
	for rows.Next() {
		var m entity.ProjectTeamMember
		var role sql.NullString
		var startDate, endDate, updatedAt sql.NullTime

		err := rows.Scan(
			&m.ID, &m.TenantID, &m.ProjectID, &m.EmployeeID, &m.EmployeeName,
			&role, &m.AllocationPercent, &startDate, &endDate, &m.CreatedAt, &updatedAt,
		)
		if err != nil {
			continue
		}

		if role.Valid {
			m.Role = &role.String
		}
		if startDate.Valid {
			m.StartDate = &startDate.Time
		}
		if endDate.Valid {
			m.EndDate = &endDate.Time
		}
		if updatedAt.Valid {
			m.UpdatedAt = updatedAt.Time
		}

		members = append(members, m.ToResponse())
	}

	response.Success(c, members)
}

// AddProjectTeamMember adds a team member to a project
func (h *Handler) AddProjectTeamMember(c *gin.Context) {
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

	var input entity.CreateTeamMemberInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	employeeID, err := uuid.Parse(input.EmployeeID)
	if err != nil {
		response.BadRequest(c, "Invalid employee_id")
		return
	}

	memberID := uuid.New()
	now := time.Now()

	// Default allocation
	allocationPercent := input.AllocationPercent
	if allocationPercent <= 0 {
		allocationPercent = 100
	}

	var role *string
	if input.Role != "" {
		role = &input.Role
	}

	var startDate, endDate *time.Time
	if input.StartDate != "" {
		sd, err := time.Parse("2006-01-02", input.StartDate)
		if err == nil {
			startDate = &sd
		}
	}
	if input.EndDate != "" {
		ed, err := time.Parse("2006-01-02", input.EndDate)
		if err == nil {
			endDate = &ed
		}
	}

	query := `
		INSERT INTO project_team_members (
			id, tenant_id, project_id, employee_id, employee_name,
			role, allocation_percent, start_date, end_date, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

	_, err = h.db.Exec(query,
		memberID, tenantID, projectID, employeeID, input.EmployeeName,
		role, allocationPercent, startDate, endDate, now, now,
	)
	if err != nil {
		h.log.Error("Failed to add team member", "error", err)
		response.InternalError(c, "Failed to add team member")
		return
	}

	member := &entity.ProjectTeamMember{
		ID:                memberID,
		TenantID:          tenantID,
		ProjectID:         projectID,
		EmployeeID:        employeeID,
		EmployeeName:      input.EmployeeName,
		Role:              role,
		AllocationPercent: allocationPercent,
		StartDate:         startDate,
		EndDate:           endDate,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	response.Created(c, member.ToResponse())
}

// RemoveProjectTeamMember removes a team member from a project
func (h *Handler) RemoveProjectTeamMember(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	memberID, err := uuid.Parse(c.Param("memberId"))
	if err != nil {
		response.BadRequest(c, "Invalid member ID")
		return
	}

	result, err := h.db.Exec(
		"DELETE FROM project_team_members WHERE id = $1 AND tenant_id = $2",
		memberID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to remove team member")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Team member")
		return
	}

	response.NoContent(c)
}
