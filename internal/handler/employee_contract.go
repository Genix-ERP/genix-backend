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

// ListEmployeeContracts returns a paginated list of employee contracts
func (h *Handler) ListEmployeeContracts(c *gin.Context) {
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
	if limit < 1 || limit > 500 {
		limit = 100
	}
	offset := (page - 1) * limit

	// Parse filters
	employeeID := c.Query("employee_id")
	contractType := c.Query("contract_type")
	status := c.Query("status")
	sortBy := c.DefaultQuery("sort_by", "created_at")
	sortOrder := c.DefaultQuery("sort_order", "DESC")

	// Build query
	baseQuery := `
		SELECT id, tenant_id, employee_id, employee_name, job_title, department,
			   contract_type, start_date, end_date, salary, currency,
			   working_hours, probation_period, notice_period, benefits, terms,
			   status, signed_date, termination_date, termination_note,
			   created_at, updated_at
		FROM employee_contracts
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM employee_contracts WHERE tenant_id = $1 AND deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	// Apply filters
	if employeeID != "" {
		if empUUID, err := uuid.Parse(employeeID); err == nil {
			argCount++
			filter := fmt.Sprintf(" AND employee_id = $%d", argCount)
			baseQuery += filter
			countQuery += filter
			args = append(args, empUUID)
		}
	}

	if contractType != "" && contractType != "all" {
		argCount++
		filter := fmt.Sprintf(" AND contract_type = $%d", argCount)
		baseQuery += filter
		countQuery += filter
		args = append(args, contractType)
	}

	if status != "" && status != "all" {
		argCount++
		filter := fmt.Sprintf(" AND status = $%d", argCount)
		baseQuery += filter
		countQuery += filter
		args = append(args, status)
	}

	// Add sorting
	validSortFields := map[string]bool{
		"start_date": true, "created_at": true, "status": true, "salary": true,
	}
	if !validSortFields[sortBy] {
		sortBy = "created_at"
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
		h.log.Error("Failed to count employee contracts", "error", err)
		response.InternalError(c, "Failed to count employee contracts")
		return
	}

	// Execute query
	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list employee contracts", "error", err)
		response.InternalError(c, "Failed to list employee contracts")
		return
	}
	defer rows.Close()

	contracts := make([]*entity.EmployeeContractResponse, 0)
	for rows.Next() {
		var contract entity.EmployeeContract
		var employeeName, jobTitle, department, benefits, terms, terminationNote sql.NullString
		var endDate, signedDate, terminationDate sql.NullTime

		err := rows.Scan(
			&contract.ID, &contract.TenantID, &contract.EmployeeID, &employeeName, &jobTitle, &department,
			&contract.ContractType, &contract.StartDate, &endDate, &contract.Salary, &contract.Currency,
			&contract.WorkingHours, &contract.ProbationPeriod, &contract.NoticePeriod, &benefits, &terms,
			&contract.Status, &signedDate, &terminationDate, &terminationNote,
			&contract.CreatedAt, &contract.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan employee contract", "error", err)
			continue
		}

		if employeeName.Valid {
			contract.EmployeeName = &employeeName.String
		}
		if jobTitle.Valid {
			contract.JobTitle = &jobTitle.String
		}
		if department.Valid {
			contract.Department = &department.String
		}
		if benefits.Valid {
			contract.Benefits = &benefits.String
		}
		if terms.Valid {
			contract.Terms = &terms.String
		}
		if terminationNote.Valid {
			contract.TerminationNote = &terminationNote.String
		}
		if endDate.Valid {
			contract.EndDate = &endDate.Time
		}
		if signedDate.Valid {
			contract.SignedDate = &signedDate.Time
		}
		if terminationDate.Valid {
			contract.TerminationDate = &terminationDate.Time
		}

		contracts = append(contracts, contract.ToResponse())
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, contracts, pagination)
}

// CreateEmployeeContract creates a new employee contract
func (h *Handler) CreateEmployeeContract(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CreateEmployeeContractInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Parse employee ID
	employeeID, err := uuid.Parse(input.EmployeeID)
	if err != nil {
		response.BadRequest(c, "Invalid employee ID")
		return
	}

	// Parse start date
	startDate, err := time.Parse("2006-01-02", input.StartDate)
	if err != nil {
		response.BadRequest(c, "Invalid start_date format. Use YYYY-MM-DD")
		return
	}

	// Parse end date (optional)
	var endDate *time.Time
	if input.EndDate != "" {
		parsed, err := time.Parse("2006-01-02", input.EndDate)
		if err == nil {
			endDate = &parsed
		}
	}

	// Parse signed date (optional)
	var signedDate *time.Time
	if input.SignedDate != "" {
		parsed, err := time.Parse("2006-01-02", input.SignedDate)
		if err == nil {
			signedDate = &parsed
		}
	}

	// Default values
	currency := input.Currency
	if currency == "" {
		currency = "UZS"
	}

	status := input.Status
	if status == "" {
		status = "active"
	}

	workingHours := input.WorkingHours
	if workingHours == 0 {
		workingHours = 40
	}

	id := uuid.New()
	now := time.Now()

	query := `
		INSERT INTO employee_contracts (
			id, tenant_id, employee_id, employee_name, job_title, department,
			contract_type, start_date, end_date, salary, currency,
			working_hours, probation_period, notice_period, benefits, terms,
			status, signed_date, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
		RETURNING id, created_at
	`

	var employeeName, jobTitle, department, benefits, terms *string
	if input.EmployeeName != "" {
		employeeName = &input.EmployeeName
	}
	if input.JobTitle != "" {
		jobTitle = &input.JobTitle
	}
	if input.Department != "" {
		department = &input.Department
	}
	if input.Benefits != "" {
		benefits = &input.Benefits
	}
	if input.Terms != "" {
		terms = &input.Terms
	}

	err = h.db.QueryRow(query,
		id, tenantID, employeeID, employeeName, jobTitle, department,
		input.ContractType, startDate, endDate, input.Salary, currency,
		workingHours, input.ProbationPeriod, input.NoticePeriod, benefits, terms,
		status, signedDate, now, now,
	).Scan(&id, &now)

	if err != nil {
		h.log.Error("Failed to create employee contract", "error", err)
		response.InternalError(c, "Failed to create employee contract")
		return
	}

	contract := &entity.EmployeeContract{
		ID:              id,
		TenantID:        tenantID,
		EmployeeID:      employeeID,
		EmployeeName:    employeeName,
		JobTitle:        jobTitle,
		Department:      department,
		ContractType:    input.ContractType,
		StartDate:       startDate,
		EndDate:         endDate,
		Salary:          input.Salary,
		Currency:        currency,
		WorkingHours:    workingHours,
		ProbationPeriod: input.ProbationPeriod,
		NoticePeriod:    input.NoticePeriod,
		Benefits:        benefits,
		Terms:           terms,
		Status:          status,
		SignedDate:      signedDate,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	response.Created(c, contract.ToResponse())
}

// GetEmployeeContract returns a single employee contract by ID
func (h *Handler) GetEmployeeContract(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid employee contract ID")
		return
	}

	query := `
		SELECT id, tenant_id, employee_id, employee_name, job_title, department,
			   contract_type, start_date, end_date, salary, currency,
			   working_hours, probation_period, notice_period, benefits, terms,
			   status, signed_date, termination_date, termination_note,
			   created_at, updated_at
		FROM employee_contracts
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`

	var contract entity.EmployeeContract
	var employeeName, jobTitle, department, benefits, terms, terminationNote sql.NullString
	var endDate, signedDate, terminationDate sql.NullTime

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&contract.ID, &contract.TenantID, &contract.EmployeeID, &employeeName, &jobTitle, &department,
		&contract.ContractType, &contract.StartDate, &endDate, &contract.Salary, &contract.Currency,
		&contract.WorkingHours, &contract.ProbationPeriod, &contract.NoticePeriod, &benefits, &terms,
		&contract.Status, &signedDate, &terminationDate, &terminationNote,
		&contract.CreatedAt, &contract.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Employee contract")
		return
	}
	if err != nil {
		h.log.Error("Failed to get employee contract", "error", err)
		response.InternalError(c, "Failed to get employee contract")
		return
	}

	if employeeName.Valid {
		contract.EmployeeName = &employeeName.String
	}
	if jobTitle.Valid {
		contract.JobTitle = &jobTitle.String
	}
	if department.Valid {
		contract.Department = &department.String
	}
	if benefits.Valid {
		contract.Benefits = &benefits.String
	}
	if terms.Valid {
		contract.Terms = &terms.String
	}
	if terminationNote.Valid {
		contract.TerminationNote = &terminationNote.String
	}
	if endDate.Valid {
		contract.EndDate = &endDate.Time
	}
	if signedDate.Valid {
		contract.SignedDate = &signedDate.Time
	}
	if terminationDate.Valid {
		contract.TerminationDate = &terminationDate.Time
	}

	response.Success(c, contract.ToResponse())
}

// UpdateEmployeeContract updates an existing employee contract
func (h *Handler) UpdateEmployeeContract(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid employee contract ID")
		return
	}

	var input entity.UpdateEmployeeContractInput
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

	if input.EmployeeName != nil {
		addUpdate("employee_name", *input.EmployeeName)
	}
	if input.JobTitle != nil {
		addUpdate("job_title", *input.JobTitle)
	}
	if input.Department != nil {
		addUpdate("department", *input.Department)
	}
	if input.ContractType != nil {
		addUpdate("contract_type", *input.ContractType)
	}
	if input.StartDate != nil {
		if parsed, err := time.Parse("2006-01-02", *input.StartDate); err == nil {
			addUpdate("start_date", parsed)
		}
	}
	if input.EndDate != nil {
		if parsed, err := time.Parse("2006-01-02", *input.EndDate); err == nil {
			addUpdate("end_date", parsed)
		}
	}
	if input.Salary != nil {
		addUpdate("salary", *input.Salary)
	}
	if input.Currency != nil {
		addUpdate("currency", *input.Currency)
	}
	if input.WorkingHours != nil {
		addUpdate("working_hours", *input.WorkingHours)
	}
	if input.ProbationPeriod != nil {
		addUpdate("probation_period", *input.ProbationPeriod)
	}
	if input.NoticePeriod != nil {
		addUpdate("notice_period", *input.NoticePeriod)
	}
	if input.Benefits != nil {
		addUpdate("benefits", *input.Benefits)
	}
	if input.Terms != nil {
		addUpdate("terms", *input.Terms)
	}
	if input.Status != nil {
		addUpdate("status", *input.Status)
	}
	if input.SignedDate != nil {
		if parsed, err := time.Parse("2006-01-02", *input.SignedDate); err == nil {
			addUpdate("signed_date", parsed)
		}
	}
	if input.TerminationDate != nil {
		if parsed, err := time.Parse("2006-01-02", *input.TerminationDate); err == nil {
			addUpdate("termination_date", parsed)
		}
	}
	if input.TerminationNote != nil {
		addUpdate("termination_note", *input.TerminationNote)
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
		UPDATE employee_contracts SET %s
		WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
		RETURNING id
	`, strings.Join(updates, ", "), argCount-1, argCount)

	var returnedID uuid.UUID
	err = h.db.QueryRow(query, args...).Scan(&returnedID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Employee contract")
		return
	}
	if err != nil {
		h.log.Error("Failed to update employee contract", "error", err)
		response.InternalError(c, "Failed to update employee contract")
		return
	}

	// Fetch updated record
	h.GetEmployeeContract(c)
}

// DeleteEmployeeContract soft-deletes an employee contract
func (h *Handler) DeleteEmployeeContract(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid employee contract ID")
		return
	}

	query := `
		UPDATE employee_contracts SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`

	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete employee contract", "error", err)
		response.InternalError(c, "Failed to delete employee contract")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Employee contract")
		return
	}

	response.NoContent(c)
}
