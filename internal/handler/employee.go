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

// ListEmployees returns a paginated list of employees
func (h *Handler) ListEmployees(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	// Parse filters
	search := c.Query("search")
	status := c.Query("status")
	department := c.Query("department")
	sortBy := c.DefaultQuery("sort_by", "hire_date")
	sortOrder := c.DefaultQuery("sort_order", "DESC")

	// Build query
	baseQuery := `
		SELECT id, tenant_id, employee_number, first_name, last_name, middle_name,
			   email, phone, mobile, job_title, hire_date, status, base_salary,
			   notes, created_at, updated_at
		FROM employees
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM employees WHERE tenant_id = $1 AND deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	// Apply filters
	if search != "" {
		argCount++
		searchFilter := fmt.Sprintf(" AND (first_name ILIKE $%d OR last_name ILIKE $%d OR email ILIKE $%d OR employee_number ILIKE $%d)", argCount, argCount, argCount, argCount)
		baseQuery += searchFilter
		countQuery += searchFilter
		args = append(args, "%"+search+"%")
	}

	if status != "" && status != "all" {
		argCount++
		statusFilter := fmt.Sprintf(" AND status = $%d", argCount)
		baseQuery += statusFilter
		countQuery += statusFilter
		args = append(args, status)
	}

	if department != "" && department != "all" {
		argCount++
		// Department is stored in notes or custom_fields for now
		deptFilter := fmt.Sprintf(" AND notes ILIKE $%d", argCount)
		baseQuery += deptFilter
		countQuery += deptFilter
		args = append(args, "%"+department+"%")
	}

	// Add sorting
	validSortFields := map[string]bool{
		"hire_date": true, "first_name": true, "last_name": true,
		"employee_number": true, "status": true, "created_at": true,
	}
	if !validSortFields[sortBy] {
		sortBy = "hire_date"
	}
	if sortOrder != "ASC" && sortOrder != "DESC" {
		sortOrder = "DESC"
	}
	baseQuery += fmt.Sprintf(" ORDER BY %s %s", sortBy, sortOrder)

	// Add pagination
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	// Get total count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count employees", "error", err)
		response.InternalError(c, "Failed to count employees")
		return
	}

	// Execute query
	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list employees", "error", err)
		response.InternalError(c, "Failed to list employees")
		return
	}
	defer rows.Close()

	employees := make([]*entity.EmployeeResponse, 0)
	for rows.Next() {
		var emp entity.Employee
		var middleName, email, phone, mobile, jobTitle, notes sql.NullString
		var baseSalary sql.NullFloat64

		err := rows.Scan(
			&emp.ID, &emp.TenantID, &emp.EmployeeNumber, &emp.FirstName, &emp.LastName,
			&middleName, &email, &phone, &mobile, &jobTitle, &emp.HireDate, &emp.Status,
			&baseSalary, &notes, &emp.CreatedAt, &emp.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan employee", "error", err)
			continue
		}

		if middleName.Valid {
			emp.MiddleName = &middleName.String
		}
		if email.Valid {
			emp.Email = &email.String
		}
		if phone.Valid {
			emp.Phone = &phone.String
		}
		if jobTitle.Valid {
			emp.JobTitle = &jobTitle.String
		}
		if baseSalary.Valid {
			emp.BaseSalary = &baseSalary.Float64
		}

		// Parse department and performance from notes (temporary storage)
		if notes.Valid {
			emp.Notes = &notes.String
			// Parse department from notes JSON-like format
			emp.Department = parseDepartmentFromNotes(notes.String)
			emp.PerformanceScore = parsePerformanceFromNotes(notes.String)
			emp.TurnoverRisk = parseTurnoverRiskFromNotes(notes.String)
		}

		employees = append(employees, emp.ToResponse())
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, employees, pagination)
}

// CreateEmployee creates a new employee
func (h *Handler) CreateEmployee(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CreateEmployeeInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Handle full_name field (split into first/last name if provided)
	firstName := input.FirstName
	lastName := input.LastName
	if input.FullName != "" && (firstName == "" || lastName == "") {
		parts := strings.Fields(input.FullName)
		if len(parts) >= 2 {
			firstName = parts[0]
			lastName = strings.Join(parts[1:], " ")
		} else if len(parts) == 1 {
			firstName = parts[0]
			lastName = ""
		}
	}

	if firstName == "" {
		response.BadRequest(c, "First name is required")
		return
	}

	// Generate employee number if not provided
	employeeNumber := input.EmployeeNumber
	if employeeNumber == "" {
		employeeNumber = fmt.Sprintf("EMP%d", time.Now().UnixNano()%100000)
	}

	// Parse hire date
	hireDate := time.Now()
	if input.HireDate != "" {
		parsed, err := time.Parse("2006-01-02", input.HireDate)
		if err == nil {
			hireDate = parsed
		}
	}

	// Default status
	status := input.Status
	if status == "" {
		status = "active"
	}

	// Store department, performance_score, turnover_risk in notes as JSON
	notes := fmt.Sprintf(`{"department":"%s","performance_score":%.1f,"turnover_risk":"%s"}`,
		input.Department, input.PerformanceScore, input.TurnoverRisk)

	id := uuid.New()
	now := time.Now()

	query := `
		INSERT INTO employees (
			id, tenant_id, employee_number, first_name, last_name, middle_name,
			email, phone, job_title, hire_date, status, base_salary, notes,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING id, created_at
	`

	var middleName, email, phone, jobTitle *string
	if input.MiddleName != "" {
		middleName = &input.MiddleName
	}
	if input.Email != "" {
		email = &input.Email
	}
	if input.Phone != "" {
		phone = &input.Phone
	}
	if input.JobTitle != "" {
		jobTitle = &input.JobTitle
	}

	var baseSalary *float64
	if input.BaseSalary > 0 {
		baseSalary = &input.BaseSalary
	}

	err := h.db.QueryRow(query,
		id, tenantID, employeeNumber, firstName, lastName, middleName,
		email, phone, jobTitle, hireDate, status, baseSalary, notes,
		now, now,
	).Scan(&id, &now)

	if err != nil {
		h.log.Error("Failed to create employee", "error", err)
		if strings.Contains(err.Error(), "duplicate") {
			response.Conflict(c, "Employee with this number already exists")
			return
		}
		response.InternalError(c, "Failed to create employee")
		return
	}

	emp := &entity.Employee{
		ID:               id,
		TenantID:         tenantID,
		EmployeeNumber:   employeeNumber,
		FirstName:        firstName,
		LastName:         lastName,
		MiddleName:       middleName,
		Email:            email,
		Phone:            phone,
		JobTitle:         jobTitle,
		HireDate:         hireDate,
		Status:           status,
		BaseSalary:       baseSalary,
		Department:       input.Department,
		PerformanceScore: input.PerformanceScore,
		TurnoverRisk:     input.TurnoverRisk,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	response.Created(c, emp.ToResponse())
}

// GetEmployee returns a single employee by ID
func (h *Handler) GetEmployee(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid employee ID")
		return
	}

	query := `
		SELECT id, tenant_id, employee_number, first_name, last_name, middle_name,
			   email, phone, mobile, job_title, hire_date, status, base_salary,
			   notes, created_at, updated_at
		FROM employees
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`

	var emp entity.Employee
	var middleName, email, phone, mobile, jobTitle, notes sql.NullString
	var baseSalary sql.NullFloat64

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&emp.ID, &emp.TenantID, &emp.EmployeeNumber, &emp.FirstName, &emp.LastName,
		&middleName, &email, &phone, &mobile, &jobTitle, &emp.HireDate, &emp.Status,
		&baseSalary, &notes, &emp.CreatedAt, &emp.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Employee")
		return
	}
	if err != nil {
		h.log.Error("Failed to get employee", "error", err)
		response.InternalError(c, "Failed to get employee")
		return
	}

	if middleName.Valid {
		emp.MiddleName = &middleName.String
	}
	if email.Valid {
		emp.Email = &email.String
	}
	if phone.Valid {
		emp.Phone = &phone.String
	}
	if jobTitle.Valid {
		emp.JobTitle = &jobTitle.String
	}
	if baseSalary.Valid {
		emp.BaseSalary = &baseSalary.Float64
	}
	if notes.Valid {
		emp.Notes = &notes.String
		emp.Department = parseDepartmentFromNotes(notes.String)
		emp.PerformanceScore = parsePerformanceFromNotes(notes.String)
		emp.TurnoverRisk = parseTurnoverRiskFromNotes(notes.String)
	}

	response.Success(c, emp.ToResponse())
}

// UpdateEmployee updates an existing employee
func (h *Handler) UpdateEmployee(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid employee ID")
		return
	}

	var input entity.UpdateEmployeeInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
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

	// Handle full_name update
	if input.FullName != nil && *input.FullName != "" {
		parts := strings.Fields(*input.FullName)
		if len(parts) >= 2 {
			firstName := parts[0]
			lastName := strings.Join(parts[1:], " ")
			addUpdate("first_name", firstName)
			addUpdate("last_name", lastName)
		} else if len(parts) == 1 {
			addUpdate("first_name", parts[0])
		}
	} else {
		if input.FirstName != nil {
			addUpdate("first_name", *input.FirstName)
		}
		if input.LastName != nil {
			addUpdate("last_name", *input.LastName)
		}
	}

	if input.Email != nil {
		addUpdate("email", *input.Email)
	}
	if input.Phone != nil {
		addUpdate("phone", *input.Phone)
	}
	if input.JobTitle != nil {
		addUpdate("job_title", *input.JobTitle)
	}
	if input.Status != nil {
		addUpdate("status", *input.Status)
	}
	if input.BaseSalary != nil {
		addUpdate("base_salary", *input.BaseSalary)
	}
	if input.HireDate != nil {
		if parsed, err := time.Parse("2006-01-02", *input.HireDate); err == nil {
			addUpdate("hire_date", parsed)
		}
	}

	// Update notes with department/performance/turnover
	if input.Department != nil || input.PerformanceScore != nil || input.TurnoverRisk != nil {
		dept := ""
		perf := 3.0
		risk := "low"
		if input.Department != nil {
			dept = *input.Department
		}
		if input.PerformanceScore != nil {
			perf = *input.PerformanceScore
		}
		if input.TurnoverRisk != nil {
			risk = *input.TurnoverRisk
		}
		notes := fmt.Sprintf(`{"department":"%s","performance_score":%.1f,"turnover_risk":"%s"}`, dept, perf, risk)
		addUpdate("notes", notes)
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
		UPDATE employees SET %s
		WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
		RETURNING id
	`, strings.Join(updates, ", "), argCount-1, argCount)

	var returnedID uuid.UUID
	err = h.db.QueryRow(query, args...).Scan(&returnedID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Employee")
		return
	}
	if err != nil {
		h.log.Error("Failed to update employee", "error", err)
		response.InternalError(c, "Failed to update employee")
		return
	}

	// Fetch updated employee
	h.GetEmployee(c)
}

// DeleteEmployee soft-deletes an employee
func (h *Handler) DeleteEmployee(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid employee ID")
		return
	}

	query := `
		UPDATE employees SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`

	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete employee", "error", err)
		response.InternalError(c, "Failed to delete employee")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Employee")
		return
	}

	response.NoContent(c)
}

// Helper functions to parse stored JSON in notes field
func parseDepartmentFromNotes(notes string) string {
	// Simple parsing of {"department":"value",...}
	if idx := strings.Index(notes, `"department":"`); idx != -1 {
		start := idx + 14
		end := strings.Index(notes[start:], `"`)
		if end != -1 {
			return notes[start : start+end]
		}
	}
	return ""
}

func parsePerformanceFromNotes(notes string) float64 {
	if idx := strings.Index(notes, `"performance_score":`); idx != -1 {
		start := idx + 20
		end := start
		for end < len(notes) && (notes[end] >= '0' && notes[end] <= '9' || notes[end] == '.') {
			end++
		}
		if val, err := strconv.ParseFloat(notes[start:end], 64); err == nil {
			return val
		}
	}
	return 3.0 // default
}

func parseTurnoverRiskFromNotes(notes string) string {
	if idx := strings.Index(notes, `"turnover_risk":"`); idx != -1 {
		start := idx + 17
		end := strings.Index(notes[start:], `"`)
		if end != -1 {
			return notes[start : start+end]
		}
	}
	return "low" // default
}
