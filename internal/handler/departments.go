package handler

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Department represents a department in the system
type Department struct {
	ID             uuid.UUID  `json:"id"`
	TenantID       uuid.UUID  `json:"tenant_id"`
	OrganizationID uuid.UUID  `json:"organization_id"`
	ParentID       *uuid.UUID `json:"parent_id,omitempty"`
	Code           string     `json:"code"`
	Name           string     `json:"name"`
	ManagerID      *uuid.UUID `json:"manager_id,omitempty"`
	CostCenter     *string    `json:"cost_center,omitempty"`
	IsActive       bool       `json:"is_active"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// CreateDepartmentInput represents input for creating a department
type CreateDepartmentInput struct {
	Code       string  `json:"code"`
	Name       string  `json:"name" binding:"required"`
	ParentID   *string `json:"parent_id,omitempty"`
	ManagerID  *string `json:"manager_id,omitempty"`
	CostCenter *string `json:"cost_center,omitempty"`
}

// UpdateDepartmentInput represents input for updating a department
type UpdateDepartmentInput struct {
	Code       *string `json:"code,omitempty"`
	Name       *string `json:"name,omitempty"`
	ParentID   *string `json:"parent_id,omitempty"`
	ManagerID  *string `json:"manager_id,omitempty"`
	CostCenter *string `json:"cost_center,omitempty"`
	IsActive   *bool   `json:"is_active,omitempty"`
}

// ListDepartments returns all departments for the tenant
func (h *Handler) ListDepartments(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	query := `
		SELECT id, tenant_id, organization_id, parent_id, code, name,
		       manager_id, cost_center, is_active, created_at, updated_at
		FROM departments
		WHERE tenant_id = $1 AND deleted_at IS NULL
		ORDER BY name ASC
	`

	args := []interface{}{tenantID}

	// Filter by organization if provided
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		query = `
			SELECT id, tenant_id, organization_id, parent_id, code, name,
			       manager_id, cost_center, is_active, created_at, updated_at
			FROM departments
			WHERE tenant_id = $1 AND organization_id = $2 AND deleted_at IS NULL
			ORDER BY name ASC
		`
		args = append(args, orgID)
	}

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list departments", "error", err)
		response.InternalError(c, "Failed to list departments")
		return
	}
	defer rows.Close()

	departments := make([]Department, 0)
	for rows.Next() {
		var dept Department
		var parentID, managerID sql.NullString
		var costCenter sql.NullString

		err := rows.Scan(
			&dept.ID, &dept.TenantID, &dept.OrganizationID, &parentID,
			&dept.Code, &dept.Name, &managerID, &costCenter,
			&dept.IsActive, &dept.CreatedAt, &dept.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan department", "error", err)
			continue
		}

		if parentID.Valid {
			pid, _ := uuid.Parse(parentID.String)
			dept.ParentID = &pid
		}
		if managerID.Valid {
			mid, _ := uuid.Parse(managerID.String)
			dept.ManagerID = &mid
		}
		if costCenter.Valid {
			dept.CostCenter = &costCenter.String
		}

		departments = append(departments, dept)
	}

	response.Success(c, departments)
}

// CreateDepartment creates a new department
func (h *Handler) CreateDepartment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	orgID, orgOk := middleware.GetOrganizationID(c)
	if !orgOk || orgID == uuid.Nil {
		response.BadRequest(c, "Organization not found")
		return
	}

	var input CreateDepartmentInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Auto-generate code from name if not provided
	if strings.TrimSpace(input.Code) == "" {
		input.Code = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(input.Name), " ", "_"))
		if len(input.Code) > 20 {
			input.Code = input.Code[:20]
		}
	}

	id := uuid.New()
	now := time.Now()

	var parentID *uuid.UUID
	if input.ParentID != nil && *input.ParentID != "" {
		pid, err := uuid.Parse(*input.ParentID)
		if err == nil {
			parentID = &pid
		}
	}

	var managerID *uuid.UUID
	if input.ManagerID != nil && *input.ManagerID != "" {
		mid, err := uuid.Parse(*input.ManagerID)
		if err == nil {
			managerID = &mid
		}
	}

	query := `
		INSERT INTO departments (id, tenant_id, organization_id, parent_id, code, name, manager_id, cost_center, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, true, $9, $9)
		RETURNING id, created_at
	`

	err := h.db.QueryRow(query,
		id, tenantID, orgID, parentID, input.Code, input.Name, managerID, input.CostCenter, now,
	).Scan(&id, &now)

	if err != nil {
		h.log.Error("Failed to create department", "error", err)
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			response.Conflict(c, "Department with this code already exists")
			return
		}
		response.InternalError(c, "Failed to create department")
		return
	}

	dept := Department{
		ID:             id,
		TenantID:       tenantID,
		OrganizationID: orgID,
		ParentID:       parentID,
		Code:           input.Code,
		Name:           input.Name,
		ManagerID:      managerID,
		CostCenter:     input.CostCenter,
		IsActive:       true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	response.Created(c, dept)
}

// GetDepartment returns a single department by ID
func (h *Handler) GetDepartment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid department ID")
		return
	}

	query := `
		SELECT id, tenant_id, organization_id, parent_id, code, name,
		       manager_id, cost_center, is_active, created_at, updated_at
		FROM departments
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`

	var dept Department
	var parentID, managerID sql.NullString
	var costCenter sql.NullString

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&dept.ID, &dept.TenantID, &dept.OrganizationID, &parentID,
		&dept.Code, &dept.Name, &managerID, &costCenter,
		&dept.IsActive, &dept.CreatedAt, &dept.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Department")
		return
	}
	if err != nil {
		h.log.Error("Failed to get department", "error", err)
		response.InternalError(c, "Failed to get department")
		return
	}

	if parentID.Valid {
		pid, _ := uuid.Parse(parentID.String)
		dept.ParentID = &pid
	}
	if managerID.Valid {
		mid, _ := uuid.Parse(managerID.String)
		dept.ManagerID = &mid
	}
	if costCenter.Valid {
		dept.CostCenter = &costCenter.String
	}

	response.Success(c, dept)
}

// UpdateDepartment updates an existing department
func (h *Handler) UpdateDepartment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid department ID")
		return
	}

	var input UpdateDepartmentInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	updates := make([]string, 0)
	args := make([]interface{}, 0)
	argCount := 0

	addUpdate := func(field string, value interface{}) {
		argCount++
		updates = append(updates, fmt.Sprintf("%s = $%d", field, argCount))
		args = append(args, value)
	}

	if input.Code != nil {
		addUpdate("code", *input.Code)
	}
	if input.Name != nil {
		addUpdate("name", *input.Name)
	}
	if input.ParentID != nil {
		if *input.ParentID == "" {
			addUpdate("parent_id", nil)
		} else {
			pid, err := uuid.Parse(*input.ParentID)
			if err == nil {
				addUpdate("parent_id", pid)
			}
		}
	}
	if input.ManagerID != nil {
		if *input.ManagerID == "" {
			addUpdate("manager_id", nil)
		} else {
			mid, err := uuid.Parse(*input.ManagerID)
			if err == nil {
				addUpdate("manager_id", mid)
			}
		}
	}
	if input.CostCenter != nil {
		addUpdate("cost_center", *input.CostCenter)
	}
	if input.IsActive != nil {
		addUpdate("is_active", *input.IsActive)
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
		UPDATE departments SET %s
		WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
		RETURNING id
	`, strings.Join(updates, ", "), argCount-1, argCount)

	var returnedID uuid.UUID
	err = h.db.QueryRow(query, args...).Scan(&returnedID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Department")
		return
	}
	if err != nil {
		h.log.Error("Failed to update department", "error", err)
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			response.Conflict(c, "Department with this code already exists")
			return
		}
		response.InternalError(c, "Failed to update department")
		return
	}

	// Return updated department
	h.GetDepartment(c)
}

// DeleteDepartment soft-deletes a department
func (h *Handler) DeleteDepartment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid department ID")
		return
	}

	query := `
		UPDATE departments SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`

	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete department", "error", err)
		response.InternalError(c, "Failed to delete department")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Department")
		return
	}

	response.NoContent(c)
}
