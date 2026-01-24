package handler

import (
	"database/sql"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ModulePermission represents a single module permission entry
type ModulePermission struct {
	ID         uuid.UUID `json:"id"`
	ModuleID   string    `json:"module_id"`
	CanCreate  bool      `json:"can_create"`
	CanRead    bool      `json:"can_read"`
	CanUpdate  bool      `json:"can_update"`
	CanDelete  bool      `json:"can_delete"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// EmployeePermissionsResponse represents the full permissions for an employee
type EmployeePermissionsResponse struct {
	EmployeeID  uuid.UUID                     `json:"employee_id"`
	Permissions map[string]*ModulePermission  `json:"permissions"`
}

// UpdateModulePermissionInput represents input for updating a single module permission
type UpdateModulePermissionInput struct {
	ModuleID  string `json:"module_id" binding:"required"`
	CanCreate bool   `json:"can_create"`
	CanRead   bool   `json:"can_read"`
	CanUpdate bool   `json:"can_update"`
	CanDelete bool   `json:"can_delete"`
}

// BulkUpdatePermissionsInput represents input for updating all permissions at once
type BulkUpdatePermissionsInput struct {
	Permissions []UpdateModulePermissionInput `json:"permissions" binding:"required"`
}

// GetEmployeePermissions returns all module permissions for an employee
func (h *Handler) GetEmployeePermissions(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	employeeIDStr := c.Param("id")
	employeeID, err := uuid.Parse(employeeIDStr)
	if err != nil {
		response.BadRequest(c, "Invalid employee ID")
		return
	}

	// Verify employee exists and belongs to tenant
	var exists bool
	err = h.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)
	`, employeeID, tenantID).Scan(&exists)
	if err != nil || !exists {
		response.NotFound(c, "Employee")
		return
	}

	// Fetch all permissions for this employee
	query := `
		SELECT id, module_id, can_create, can_read, can_update, can_delete, created_at, updated_at
		FROM employee_module_permissions
		WHERE tenant_id = $1 AND employee_id = $2
	`

	rows, err := h.db.Query(query, tenantID, employeeID)
	if err != nil {
		h.log.Error("Failed to fetch employee permissions", "error", err)
		response.InternalError(c, "Failed to fetch permissions")
		return
	}
	defer rows.Close()

	permissions := make(map[string]*ModulePermission)
	for rows.Next() {
		var perm ModulePermission
		err := rows.Scan(&perm.ID, &perm.ModuleID, &perm.CanCreate, &perm.CanRead, &perm.CanUpdate, &perm.CanDelete, &perm.CreatedAt, &perm.UpdatedAt)
		if err != nil {
			h.log.Error("Failed to scan permission", "error", err)
			continue
		}
		permissions[perm.ModuleID] = &perm
	}

	result := EmployeePermissionsResponse{
		EmployeeID:  employeeID,
		Permissions: permissions,
	}

	response.Success(c, result)
}

// UpdateEmployeeModulePermission updates a single module permission for an employee
func (h *Handler) UpdateEmployeeModulePermission(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	employeeIDStr := c.Param("id")
	employeeID, err := uuid.Parse(employeeIDStr)
	if err != nil {
		response.BadRequest(c, "Invalid employee ID")
		return
	}

	var input UpdateModulePermissionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Verify employee exists and belongs to tenant
	var exists bool
	err = h.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)
	`, employeeID, tenantID).Scan(&exists)
	if err != nil || !exists {
		response.NotFound(c, "Employee")
		return
	}

	now := time.Now()

	// Upsert the permission
	query := `
		INSERT INTO employee_module_permissions (tenant_id, employee_id, module_id, can_create, can_read, can_update, can_delete, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
		ON CONFLICT (tenant_id, employee_id, module_id)
		DO UPDATE SET can_create = $4, can_read = $5, can_update = $6, can_delete = $7, updated_at = $8
		RETURNING id, module_id, can_create, can_read, can_update, can_delete, created_at, updated_at
	`

	var perm ModulePermission
	err = h.db.QueryRow(query, tenantID, employeeID, input.ModuleID, input.CanCreate, input.CanRead, input.CanUpdate, input.CanDelete, now).
		Scan(&perm.ID, &perm.ModuleID, &perm.CanCreate, &perm.CanRead, &perm.CanUpdate, &perm.CanDelete, &perm.CreatedAt, &perm.UpdatedAt)
	if err != nil {
		h.log.Error("Failed to update module permission", "error", err)
		response.InternalError(c, "Failed to update permission")
		return
	}

	response.Success(c, perm)
}

// BulkUpdateEmployeePermissions updates all module permissions for an employee at once
func (h *Handler) BulkUpdateEmployeePermissions(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	employeeIDStr := c.Param("id")
	employeeID, err := uuid.Parse(employeeIDStr)
	if err != nil {
		response.BadRequest(c, "Invalid employee ID")
		return
	}

	var input BulkUpdatePermissionsInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Verify employee exists and belongs to tenant
	var exists bool
	err = h.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)
	`, employeeID, tenantID).Scan(&exists)
	if err != nil || !exists {
		response.NotFound(c, "Employee")
		return
	}

	now := time.Now()

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to update permissions")
		return
	}
	defer tx.Rollback()

	// Delete existing permissions for this employee
	_, err = tx.Exec(`DELETE FROM employee_module_permissions WHERE tenant_id = $1 AND employee_id = $2`, tenantID, employeeID)
	if err != nil {
		h.log.Error("Failed to delete existing permissions", "error", err)
		response.InternalError(c, "Failed to update permissions")
		return
	}

	// Insert new permissions
	insertQuery := `
		INSERT INTO employee_module_permissions (tenant_id, employee_id, module_id, can_create, can_read, can_update, can_delete, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
	`

	permissions := make(map[string]*ModulePermission)
	for _, p := range input.Permissions {
		id := uuid.New()
		_, err = tx.Exec(insertQuery, tenantID, employeeID, p.ModuleID, p.CanCreate, p.CanRead, p.CanUpdate, p.CanDelete, now)
		if err != nil {
			h.log.Error("Failed to insert permission", "error", err, "module", p.ModuleID)
			response.InternalError(c, "Failed to update permissions")
			return
		}
		permissions[p.ModuleID] = &ModulePermission{
			ID:        id,
			ModuleID:  p.ModuleID,
			CanCreate: p.CanCreate,
			CanRead:   p.CanRead,
			CanUpdate: p.CanUpdate,
			CanDelete: p.CanDelete,
			CreatedAt: now,
			UpdatedAt: now,
		}
	}

	// Commit transaction
	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to update permissions")
		return
	}

	result := EmployeePermissionsResponse{
		EmployeeID:  employeeID,
		Permissions: permissions,
	}

	response.Success(c, result)
}

// DeleteEmployeePermissions deletes all permissions for an employee
func (h *Handler) DeleteEmployeePermissions(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	employeeIDStr := c.Param("id")
	employeeID, err := uuid.Parse(employeeIDStr)
	if err != nil {
		response.BadRequest(c, "Invalid employee ID")
		return
	}

	_, err = h.db.Exec(`
		DELETE FROM employee_module_permissions WHERE tenant_id = $1 AND employee_id = $2
	`, tenantID, employeeID)
	if err != nil {
		h.log.Error("Failed to delete employee permissions", "error", err)
		response.InternalError(c, "Failed to delete permissions")
		return
	}

	response.NoContent(c)
}

// GetCurrentUserPermissions returns permissions for the currently authenticated user's linked employee
func (h *Handler) GetCurrentUserPermissions(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Get the current user from context
	userID, ok := middleware.GetUserID(c)
	if !ok || userID == uuid.Nil {
		response.Unauthorized(c, "User not found")
		return
	}

	// Get user details including role and linked employee
	var role string
	var employeeID sql.NullString
	err := h.db.QueryRow(`
		SELECT role, employee_id FROM users WHERE id = $1 AND tenant_id = $2
	`, userID, tenantID).Scan(&role, &employeeID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "User")
		return
	}
	if err != nil {
		h.log.Error("Failed to get user", "error", err)
		response.InternalError(c, "Failed to get user")
		return
	}

	// Site admins, owners, and system admins have full access
	// Note: "admin" role is NOT included - they should have module-based permissions like regular users
	if role == "site_admin" || role == "owner" || role == "system_admin" {
		response.Success(c, gin.H{
			"is_admin": true,
			"permissions": nil,
		})
		return
	}

	// If no linked employee, return empty permissions
	if !employeeID.Valid {
		response.Success(c, gin.H{
			"is_admin": false,
			"permissions": map[string]interface{}{},
		})
		return
	}

	empID, err := uuid.Parse(employeeID.String)
	if err != nil {
		response.Success(c, gin.H{
			"is_admin": false,
			"permissions": map[string]interface{}{},
		})
		return
	}

	// Fetch permissions for the linked employee
	query := `
		SELECT id, module_id, can_create, can_read, can_update, can_delete, created_at, updated_at
		FROM employee_module_permissions
		WHERE tenant_id = $1 AND employee_id = $2
	`

	rows, err := h.db.Query(query, tenantID, empID)
	if err != nil {
		h.log.Error("Failed to fetch user permissions", "error", err)
		response.InternalError(c, "Failed to fetch permissions")
		return
	}
	defer rows.Close()

	permissions := make(map[string]*ModulePermission)
	for rows.Next() {
		var perm ModulePermission
		err := rows.Scan(&perm.ID, &perm.ModuleID, &perm.CanCreate, &perm.CanRead, &perm.CanUpdate, &perm.CanDelete, &perm.CreatedAt, &perm.UpdatedAt)
		if err != nil {
			h.log.Error("Failed to scan permission", "error", err)
			continue
		}
		permissions[perm.ModuleID] = &perm
	}

	// Fetch organization IDs this employee has access to
	orgQuery := `
		SELECT organization_id FROM employee_organizations
		WHERE tenant_id = $1 AND employee_id = $2
	`
	orgRows, err := h.db.Query(orgQuery, tenantID, empID)
	if err != nil {
		h.log.Error("Failed to fetch employee organizations", "error", err)
		// Don't fail the request, just return empty org list
	}

	var organizationIDs []string
	if orgRows != nil {
		defer orgRows.Close()
		for orgRows.Next() {
			var orgID uuid.UUID
			if err := orgRows.Scan(&orgID); err == nil {
				organizationIDs = append(organizationIDs, orgID.String())
			}
		}
	}

	response.Success(c, gin.H{
		"is_admin":         false,
		"employee_id":      empID,
		"permissions":      permissions,
		"organization_ids": organizationIDs,
	})
}
