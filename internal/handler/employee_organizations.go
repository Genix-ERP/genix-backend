package handler

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// EmployeeOrganization represents an employee's assignment to an organization
type EmployeeOrganization struct {
	ID               uuid.UUID  `json:"id"`
	TenantID         uuid.UUID  `json:"tenant_id"`
	EmployeeID       uuid.UUID  `json:"employee_id"`
	OrganizationID   uuid.UUID  `json:"organization_id"`
	OrganizationName string     `json:"organization_name,omitempty"`
	OrganizationCode string     `json:"organization_code,omitempty"`
	EmployeeName     string     `json:"employee_name,omitempty"`
	Role             *string    `json:"role,omitempty"`
	IsPrimary        bool       `json:"is_primary"`
	StartDate        *time.Time `json:"start_date,omitempty"`
	EndDate          *time.Time `json:"end_date,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// AssignEmployeeToOrgInput represents the input for assigning an employee to an organization
type AssignEmployeeToOrgInput struct {
	EmployeeID     string  `json:"employee_id" binding:"required"`
	OrganizationID string  `json:"organization_id" binding:"required"`
	Role           *string `json:"role,omitempty"`
	IsPrimary      bool    `json:"is_primary"`
	StartDate      *string `json:"start_date,omitempty"`
	EndDate        *string `json:"end_date,omitempty"`
}

// UpdateEmployeeOrgInput represents the input for updating an assignment
type UpdateEmployeeOrgInput struct {
	Role      *string `json:"role,omitempty"`
	IsPrimary *bool   `json:"is_primary,omitempty"`
	StartDate *string `json:"start_date,omitempty"`
	EndDate   *string `json:"end_date,omitempty"`
}

// ListEmployeeOrganizations returns all organization assignments for an employee
func (h *Handler) ListEmployeeOrganizations(c *gin.Context) {
	tenantID, exists := c.Get(middleware.ContextKeyTenantID)
	if !exists {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	employeeID := c.Param("id")
	if employeeID == "" {
		response.BadRequest(c, "Employee ID is required")
		return
	}

	query := `
		SELECT eo.id, eo.tenant_id, eo.employee_id, eo.organization_id,
		       o.name as organization_name, o.code as organization_code,
		       eo.role, eo.is_primary, eo.start_date, eo.end_date,
		       eo.created_at, eo.updated_at
		FROM employee_organizations eo
		JOIN organizations o ON o.id = eo.organization_id
		WHERE eo.tenant_id = $1 AND eo.employee_id = $2
		ORDER BY eo.is_primary DESC, o.name ASC
	`

	args := []interface{}{tenantID, employeeID}
	paginate, page, pageSize, offset := optPagination(c)
	if paginate {
		query += " LIMIT $3 OFFSET $4"
		args = append(args, pageSize, offset)
	}

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list employee organizations", "error", err)
		response.InternalServerError(c, "Failed to list employee organizations")
		return
	}
	defer rows.Close()

	var assignments []EmployeeOrganization
	for rows.Next() {
		var eo EmployeeOrganization
		err := rows.Scan(
			&eo.ID, &eo.TenantID, &eo.EmployeeID, &eo.OrganizationID,
			&eo.OrganizationName, &eo.OrganizationCode,
			&eo.Role, &eo.IsPrimary, &eo.StartDate, &eo.EndDate,
			&eo.CreatedAt, &eo.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan employee organization", "error", err)
			continue
		}
		assignments = append(assignments, eo)
	}

	if assignments == nil {
		assignments = []EmployeeOrganization{}
	}

	if !paginate {
		response.Success(c, assignments)
		return
	}

	total := 0
	if err := h.db.QueryRow(
		`SELECT COUNT(*) FROM employee_organizations WHERE tenant_id = $1 AND employee_id = $2`,
		tenantID, employeeID,
	).Scan(&total); err != nil {
		h.log.Error("Failed to count employee organizations", "error", err)
		total = len(assignments)
	}
	response.Paginated(c, assignments, page, pageSize, total)
}

// ListOrganizationEmployees returns all employees assigned to an organization
func (h *Handler) ListOrganizationEmployees(c *gin.Context) {
	tenantID, exists := c.Get(middleware.ContextKeyTenantID)
	if !exists {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	orgID := c.Param("id")
	if orgID == "" {
		response.BadRequest(c, "Organization ID is required")
		return
	}

	query := `
		SELECT eo.id, eo.tenant_id, eo.employee_id, eo.organization_id,
		       TRIM(COALESCE(e.first_name,'') || ' ' || COALESCE(e.last_name,'')) as employee_name,
		       eo.role, eo.is_primary, eo.start_date, eo.end_date,
		       eo.created_at, eo.updated_at
		FROM employee_organizations eo
		JOIN employees e ON e.id = eo.employee_id AND e.deleted_at IS NULL
		WHERE eo.tenant_id = $1 AND eo.organization_id = $2
		ORDER BY eo.is_primary DESC, employee_name ASC
	`

	args := []interface{}{tenantID, orgID}
	paginate, page, pageSize, offset := optPagination(c)
	if paginate {
		query += " LIMIT $3 OFFSET $4"
		args = append(args, pageSize, offset)
	}

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list organization employees", "error", err)
		response.InternalServerError(c, "Failed to list organization employees")
		return
	}
	defer rows.Close()

	var assignments []EmployeeOrganization
	for rows.Next() {
		var eo EmployeeOrganization
		err := rows.Scan(
			&eo.ID, &eo.TenantID, &eo.EmployeeID, &eo.OrganizationID,
			&eo.EmployeeName,
			&eo.Role, &eo.IsPrimary, &eo.StartDate, &eo.EndDate,
			&eo.CreatedAt, &eo.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan organization employee", "error", err)
			continue
		}
		assignments = append(assignments, eo)
	}

	if assignments == nil {
		assignments = []EmployeeOrganization{}
	}

	if !paginate {
		response.Success(c, assignments)
		return
	}

	total := 0
	if err := h.db.QueryRow(
		`SELECT COUNT(*) FROM employee_organizations WHERE tenant_id = $1 AND organization_id = $2`,
		tenantID, orgID,
	).Scan(&total); err != nil {
		h.log.Error("Failed to count organization employees", "error", err)
		total = len(assignments)
	}
	response.Paginated(c, assignments, page, pageSize, total)
}

// AssignEmployeeToOrganization assigns an employee to an organization
func (h *Handler) AssignEmployeeToOrganization(c *gin.Context) {
	tenantID, exists := c.Get(middleware.ContextKeyTenantID)
	if !exists {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input AssignEmployeeToOrgInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	employeeID, err := uuid.Parse(input.EmployeeID)
	if err != nil {
		response.BadRequest(c, "Invalid employee ID")
		return
	}

	orgID, err := uuid.Parse(input.OrganizationID)
	if err != nil {
		response.BadRequest(c, "Invalid organization ID")
		return
	}

	// Check if assignment already exists
	var existingCount int
	err = h.db.QueryRow(
		"SELECT COUNT(*) FROM employee_organizations WHERE tenant_id = $1 AND employee_id = $2 AND organization_id = $3",
		tenantID, employeeID, orgID,
	).Scan(&existingCount)
	if err != nil {
		h.log.Error("Failed to check existing assignment", "error", err)
		response.InternalServerError(c, "Failed to assign employee")
		return
	}
	if existingCount > 0 {
		response.Conflict(c, "Employee is already assigned to this organization")
		return
	}

	// If this is set as primary, unset other primary assignments for this employee
	if input.IsPrimary {
		_, err = h.db.Exec(
			"UPDATE employee_organizations SET is_primary = false WHERE tenant_id = $1 AND employee_id = $2",
			tenantID, employeeID,
		)
		if err != nil {
			h.log.Error("Failed to unset primary assignments", "error", err)
		}
	}

	// Parse dates if provided
	var startDate, endDate *time.Time
	if input.StartDate != nil && *input.StartDate != "" {
		if t, err := time.Parse("2006-01-02", *input.StartDate); err == nil {
			startDate = &t
		}
	}
	if input.EndDate != nil && *input.EndDate != "" {
		if t, err := time.Parse("2006-01-02", *input.EndDate); err == nil {
			endDate = &t
		}
	}

	id := uuid.New()
	now := time.Now()

	query := `
		INSERT INTO employee_organizations (id, tenant_id, employee_id, organization_id, role, is_primary, start_date, end_date, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, tenant_id, employee_id, organization_id, role, is_primary, start_date, end_date, created_at, updated_at
	`

	var eo EmployeeOrganization
	err = h.db.QueryRow(
		query,
		id, tenantID, employeeID, orgID, input.Role, input.IsPrimary, startDate, endDate, now, now,
	).Scan(
		&eo.ID, &eo.TenantID, &eo.EmployeeID, &eo.OrganizationID,
		&eo.Role, &eo.IsPrimary, &eo.StartDate, &eo.EndDate,
		&eo.CreatedAt, &eo.UpdatedAt,
	)
	if err != nil {
		h.log.Error("Failed to assign employee to organization", "error", err)
		response.InternalServerError(c, "Failed to assign employee")
		return
	}

	response.Created(c, eo)
}

// UpdateEmployeeOrganization updates an employee's organization assignment
func (h *Handler) UpdateEmployeeOrganization(c *gin.Context) {
	tenantID, exists := c.Get(middleware.ContextKeyTenantID)
	if !exists {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	assignmentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid assignment ID")
		return
	}

	var input UpdateEmployeeOrgInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Get current assignment
	var employeeID uuid.UUID
	err = h.db.QueryRow(
		"SELECT employee_id FROM employee_organizations WHERE id = $1 AND tenant_id = $2",
		assignmentID, tenantID,
	).Scan(&employeeID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Assignment")
		return
	}
	if err != nil {
		h.log.Error("Failed to get assignment", "error", err)
		response.InternalServerError(c, "Failed to update assignment")
		return
	}

	// If setting as primary, unset other primary assignments
	if input.IsPrimary != nil && *input.IsPrimary {
		_, err = h.db.Exec(
			"UPDATE employee_organizations SET is_primary = false WHERE tenant_id = $1 AND employee_id = $2 AND id != $3",
			tenantID, employeeID, assignmentID,
		)
		if err != nil {
			h.log.Error("Failed to unset primary assignments", "error", err)
		}
	}

	// Build update query
	query := "UPDATE employee_organizations SET updated_at = $1"
	args := []interface{}{time.Now()}
	argIndex := 2

	if input.Role != nil {
		query += ", role = $" + string(rune('0'+argIndex))
		args = append(args, *input.Role)
		argIndex++
	}
	if input.IsPrimary != nil {
		query += ", is_primary = $" + string(rune('0'+argIndex))
		args = append(args, *input.IsPrimary)
		argIndex++
	}
	if input.StartDate != nil {
		var startDate *time.Time
		if *input.StartDate != "" {
			if t, err := time.Parse("2006-01-02", *input.StartDate); err == nil {
				startDate = &t
			}
		}
		query += ", start_date = $" + string(rune('0'+argIndex))
		args = append(args, startDate)
		argIndex++
	}
	if input.EndDate != nil {
		var endDate *time.Time
		if *input.EndDate != "" {
			if t, err := time.Parse("2006-01-02", *input.EndDate); err == nil {
				endDate = &t
			}
		}
		query += ", end_date = $" + string(rune('0'+argIndex))
		args = append(args, endDate)
		argIndex++
	}

	query += " WHERE id = $" + string(rune('0'+argIndex)) + " AND tenant_id = $" + string(rune('0'+argIndex+1))
	args = append(args, assignmentID, tenantID)

	query += " RETURNING id, tenant_id, employee_id, organization_id, role, is_primary, start_date, end_date, created_at, updated_at"

	var eo EmployeeOrganization
	err = h.db.QueryRow(query, args...).Scan(
		&eo.ID, &eo.TenantID, &eo.EmployeeID, &eo.OrganizationID,
		&eo.Role, &eo.IsPrimary, &eo.StartDate, &eo.EndDate,
		&eo.CreatedAt, &eo.UpdatedAt,
	)
	if err != nil {
		h.log.Error("Failed to update assignment", "error", err)
		response.InternalServerError(c, "Failed to update assignment")
		return
	}

	response.Success(c, eo)
}

// RemoveEmployeeFromOrganization removes an employee from an organization
func (h *Handler) RemoveEmployeeFromOrganization(c *gin.Context) {
	tenantID, exists := c.Get(middleware.ContextKeyTenantID)
	if !exists {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	assignmentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid assignment ID")
		return
	}

	result, err := h.db.Exec(
		"DELETE FROM employee_organizations WHERE id = $1 AND tenant_id = $2",
		assignmentID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to remove employee from organization", "error", err)
		response.InternalServerError(c, "Failed to remove assignment")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Assignment")
		return
	}

	c.JSON(http.StatusNoContent, nil)
}
