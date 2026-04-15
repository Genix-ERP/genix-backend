package handler

import (
	"database/sql"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/crypto"
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
		SELECT e.id, e.tenant_id, e.employee_number, e.first_name, e.last_name, e.middle_name,
			   e.email, e.phone, e.mobile, e.job_title, e.hire_date, e.status, e.base_salary, e.permission,
			   e.notes, e.created_at, e.updated_at, e.department_id, COALESCE(d.name, '') as department_name,
			   e.job_position_id, COALESCE(jp.name, '') as job_position_name
		FROM employees e
		LEFT JOIN departments d ON e.department_id = d.id
		LEFT JOIN job_positions jp ON e.job_position_id = jp.id
		WHERE e.tenant_id = $1 AND e.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM employees e WHERE e.tenant_id = $1 AND e.deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization - use employee_organizations to show all employees with access to this company
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		orgFilter := fmt.Sprintf(" AND (e.organization_id = $%d OR e.id IN (SELECT employee_id FROM employee_organizations WHERE organization_id = $%d))", argCount, argCount)
		baseQuery += orgFilter
		countQuery += orgFilter
		args = append(args, orgID)
	}

	// Apply filters
	if search != "" {
		argCount++
		searchFilter := fmt.Sprintf(" AND (e.first_name ILIKE $%d OR e.last_name ILIKE $%d OR e.email ILIKE $%d OR e.employee_number ILIKE $%d)", argCount, argCount, argCount, argCount)
		baseQuery += searchFilter
		countQuery += searchFilter
		args = append(args, "%"+search+"%")
	}

	if status != "" && status != "all" {
		argCount++
		statusFilter := fmt.Sprintf(" AND e.status = $%d", argCount)
		baseQuery += statusFilter
		countQuery += statusFilter
		args = append(args, status)
	}

	if department != "" && department != "all" {
		argCount++
		// Filter by department_id (UUID)
		deptFilter := fmt.Sprintf(" AND e.department_id = $%d", argCount)
		baseQuery += deptFilter
		countQuery += deptFilter
		args = append(args, department)
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
	baseQuery += fmt.Sprintf(" ORDER BY e.%s %s", sortBy, sortOrder)

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
		var middleName, email, phone, mobile, jobTitle, permission, notes sql.NullString
		var baseSalary sql.NullFloat64
		var departmentID sql.NullString
		var departmentName string
		var jobPositionID sql.NullString
		var jobPositionName string

		err := rows.Scan(
			&emp.ID, &emp.TenantID, &emp.EmployeeNumber, &emp.FirstName, &emp.LastName,
			&middleName, &email, &phone, &mobile, &jobTitle, &emp.HireDate, &emp.Status,
			&baseSalary, &permission, &notes, &emp.CreatedAt, &emp.UpdatedAt,
			&departmentID, &departmentName,
			&jobPositionID, &jobPositionName,
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
		if permission.Valid {
			emp.Permission = &permission.String
		}

		// Set job_position_id from JOIN result
		if jobPositionID.Valid {
			jpUUID, _ := uuid.Parse(jobPositionID.String)
			emp.JobPositionID = &jpUUID
		}

		// Set department from JOIN result, fall back to notes parsing for legacy data
		if departmentID.Valid {
			deptUUID, _ := uuid.Parse(departmentID.String)
			emp.DepartmentID = &deptUUID
		}
		if departmentName != "" {
			emp.Department = departmentName
		} else if notes.Valid {
			// Legacy fallback: parse department from notes JSON
			emp.Department = parseDepartmentFromNotes(notes.String)
		}
		if notes.Valid {
			emp.Notes = &notes.String
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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

	// Check for duplicate phone
	if input.Phone != "" {
		var existingID uuid.UUID
		dupErr := h.db.QueryRow(
			"SELECT id FROM employees WHERE tenant_id = $1 AND phone = $2 AND deleted_at IS NULL",
			tenantID, normalizePhone(input.Phone),
		).Scan(&existingID)
		if dupErr == nil {
			response.Conflict(c, "Employee with this phone number already exists")
			return
		}
	}

	id := uuid.New()
	now := time.Now()

	// Get organization ID from context
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	// Resolve department_id from department name
	var departmentID *uuid.UUID
	if input.Department != "" {
		// Try parsing as UUID first
		if deptUUID, parseErr := uuid.Parse(input.Department); parseErr == nil {
			departmentID = &deptUUID
		} else {
			// Look up department by name
			var deptID uuid.UUID
			lookupErr := h.db.QueryRow(
				"SELECT id FROM departments WHERE tenant_id = $1 AND name = $2 AND deleted_at IS NULL LIMIT 1",
				tenantID, input.Department,
			).Scan(&deptID)
			if lookupErr == nil {
				departmentID = &deptID
			}
		}
	}

	// Resolve job_position_id
	var jobPositionID *uuid.UUID
	if input.JobPositionID != "" {
		if jpUUID, parseErr := uuid.Parse(input.JobPositionID); parseErr == nil {
			jobPositionID = &jpUUID
		}
	}

	query := `
		INSERT INTO employees (
			id, tenant_id, organization_id, department_id, job_position_id, employee_number, first_name, last_name, middle_name,
			email, phone, job_title, hire_date, status, base_salary, permission, notes,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
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
		normalizedPhone := normalizePhone(input.Phone)
		phone = &normalizedPhone
	}
	if input.JobTitle != "" {
		jobTitle = &input.JobTitle
	}

	var baseSalary *float64
	if input.BaseSalary > 0 {
		baseSalary = &input.BaseSalary
	}

	var permission *string
	if input.Permission != "" {
		permission = &input.Permission
	}

	err := h.db.QueryRow(query,
		id, tenantID, orgIDPtr, departmentID, jobPositionID, employeeNumber, firstName, lastName, middleName,
		email, phone, jobTitle, hireDate, status, baseSalary, permission, notes,
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

	// Create or link user account for this employee
	contact := input.Email
	isEmail := contact != ""
	if !isEmail && input.Phone != "" {
		contact = normalizePhone(input.Phone)
	}

	if contact != "" {
		// Check if user already exists
		var existingUserID uuid.UUID
		var lookupErr error
		if isEmail {
			lookupErr = h.db.QueryRow(
				"SELECT id FROM users WHERE tenant_id = $1 AND email = $2 AND deleted_at IS NULL",
				tenantID, contact,
			).Scan(&existingUserID)
		} else {
			lookupErr = h.db.QueryRow(
				"SELECT id FROM users WHERE tenant_id = $1 AND phone = $2 AND deleted_at IS NULL",
				tenantID, contact,
			).Scan(&existingUserID)
		}

		if lookupErr == sql.ErrNoRows {
			// User doesn't exist — create one
			const chars = "ABCDEFGHJKMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789"
			b := make([]byte, 8)
			for i := range b {
				b[i] = chars[rand.Intn(len(chars))]
			}
			plainPassword := string(b)
			passwordHash, _ := crypto.HashPassword(plainPassword)

			newUserID := uuid.New()
			var emailVal, phoneVal *string
			if isEmail {
				emailVal = &contact
				if input.Phone != "" {
					p := normalizePhone(input.Phone)
					phoneVal = &p
				}
			} else {
				phoneVal = &contact
			}

			_, createErr := h.db.Exec(`
				INSERT INTO users (id, tenant_id, email, password_hash, first_name, last_name, phone,
				                   language, timezone, settings, is_active, is_verified, employee_id)
				VALUES ($1, $2, $3, $4, $5, $6, $7, 'uz', 'Asia/Tashkent', '{}', true, true, $8)
				ON CONFLICT DO NOTHING
			`, newUserID, tenantID, emailVal, passwordHash, firstName, lastName, phoneVal, id)
			if createErr != nil {
				h.log.Warn("Could not auto-create user for employee", "error", createErr)
			}
		} else if lookupErr == nil {
			// User exists — link to this employee
			h.db.Exec(`UPDATE users SET employee_id = $1, updated_at = $2 WHERE id = $3`,
				id, now, existingUserID)
		}
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
			   email, phone, mobile, job_title, hire_date, status, base_salary, permission,
			   notes, created_at, updated_at
		FROM employees
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`

	var emp entity.Employee
	var middleName, email, phone, mobile, jobTitle, permission, notes sql.NullString
	var baseSalary sql.NullFloat64

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&emp.ID, &emp.TenantID, &emp.EmployeeNumber, &emp.FirstName, &emp.LastName,
		&middleName, &email, &phone, &mobile, &jobTitle, &emp.HireDate, &emp.Status,
		&baseSalary, &permission, &notes, &emp.CreatedAt, &emp.UpdatedAt,
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
	if permission.Valid {
		emp.Permission = &permission.String
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
	if input.Permission != nil {
		addUpdate("permission", *input.Permission)
	}

	// Update job_position_id
	if input.JobPositionID != nil && *input.JobPositionID != "" {
		if jpUUID, parseErr := uuid.Parse(*input.JobPositionID); parseErr == nil {
			addUpdate("job_position_id", jpUUID)
		}
	}

	// Update department_id (also accept department_id field directly)
	if input.DepartmentID != nil && *input.DepartmentID != "" {
		if deptUUID, parseErr := uuid.Parse(*input.DepartmentID); parseErr == nil {
			addUpdate("department_id", deptUUID)
		}
	} else if input.Department != nil && *input.Department != "" {
		// Try parsing as UUID first
		if deptUUID, parseErr := uuid.Parse(*input.Department); parseErr == nil {
			addUpdate("department_id", deptUUID)
		} else {
			// Look up department by name
			var deptID uuid.UUID
			lookupErr := h.db.QueryRow(
				"SELECT id FROM departments WHERE tenant_id = $1 AND name = $2 AND deleted_at IS NULL LIMIT 1",
				tenantID, *input.Department,
			).Scan(&deptID)
			if lookupErr == nil {
				addUpdate("department_id", deptID)
			}
		}
	}

	// Update notes with performance/turnover
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

	// Fetch employee email before deleting (needed for user cleanup)
	var empEmail sql.NullString
	h.db.QueryRow(`SELECT email FROM employees WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, id, tenantID).Scan(&empEmail)

	result, err := h.db.Exec(`
		UPDATE employees SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`, time.Now(), id, tenantID)
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

	// Delete linked user by employee_id
	h.db.Exec(`DELETE FROM users WHERE tenant_id = $1 AND employee_id = $2`, tenantID, id)

	// Also delete by email as fallback (covers cases where employee_id link was not set)
	if empEmail.Valid && empEmail.String != "" {
		h.db.Exec(`DELETE FROM users WHERE tenant_id = $1 AND email = $2`, tenantID, empEmail.String)
	}

	// Clean up employee permissions and organization assignments
	h.db.Exec(`DELETE FROM employee_module_permissions WHERE tenant_id = $1 AND employee_id = $2`, tenantID, id)
	h.db.Exec(`DELETE FROM employee_organizations WHERE tenant_id = $1 AND employee_id = $2`, tenantID, id)

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
