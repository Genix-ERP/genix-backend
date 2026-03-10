package handler

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RolePermissions represents module-level CRUD permissions stored in role's JSONB
type RolePermissions map[string]RoleModulePerm

// RoleModulePerm represents CRUD flags for a single module
type RoleModulePerm struct {
	CanCreate bool `json:"can_create"`
	CanRead   bool `json:"can_read"`
	CanUpdate bool `json:"can_update"`
	CanDelete bool `json:"can_delete"`
}

// RoleResponse represents the API response for a role
type RoleResponse struct {
	ID          uuid.UUID       `json:"id"`
	Name        string          `json:"name"`
	Code        string          `json:"code"`
	Description *string         `json:"description,omitempty"`
	IsSystem    bool            `json:"is_system"`
	Permissions RolePermissions `json:"permissions"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// CreateRoleInput represents input for creating a role
type CreateRoleInput struct {
	Name        string          `json:"name" binding:"required"`
	Code        string          `json:"code" binding:"required"`
	Description string          `json:"description"`
	Permissions RolePermissions `json:"permissions"`
}

// UpdateRoleInput represents input for updating a role
type UpdateRoleInput struct {
	Name        *string          `json:"name"`
	Description *string          `json:"description"`
	Permissions *RolePermissions `json:"permissions"`
}

// ListRoles returns all roles for the tenant
func (h *Handler) ListRoles(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	rows, err := h.db.Query(`
		SELECT id, name, code, description, is_system, permissions, created_at, updated_at
		FROM roles
		WHERE tenant_id = $1
		ORDER BY created_at ASC
	`, tenantID)
	if err != nil {
		h.log.Error("Failed to list roles", "error", err)
		response.InternalError(c, "Failed to list roles")
		return
	}
	defer rows.Close()

	roles := []RoleResponse{}
	for rows.Next() {
		var role RoleResponse
		var desc sql.NullString
		var permsJSON []byte

		err := rows.Scan(&role.ID, &role.Name, &role.Code, &desc, &role.IsSystem, &permsJSON, &role.CreatedAt, &role.UpdatedAt)
		if err != nil {
			h.log.Error("Failed to scan role", "error", err)
			continue
		}

		if desc.Valid {
			role.Description = &desc.String
		}

		role.Permissions = make(RolePermissions)
		if len(permsJSON) > 0 {
			json.Unmarshal(permsJSON, &role.Permissions)
		}

		roles = append(roles, role)
	}

	response.Success(c, roles)
}

// CreateRole creates a new role
func (h *Handler) CreateRole(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input CreateRoleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	input.Code = strings.ToLower(strings.TrimSpace(input.Code))
	input.Name = strings.TrimSpace(input.Name)

	if input.Permissions == nil {
		input.Permissions = make(RolePermissions)
	}

	permsJSON, err := json.Marshal(input.Permissions)
	if err != nil {
		response.InternalError(c, "Failed to encode permissions")
		return
	}

	now := time.Now()
	id := uuid.New()

	var role RoleResponse
	var desc sql.NullString

	err = h.db.QueryRow(`
		INSERT INTO roles (id, tenant_id, name, code, description, is_system, permissions, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, false, $6, $7, $7)
		RETURNING id, name, code, description, is_system, created_at, updated_at
	`, id, tenantID, input.Name, input.Code, input.Description, permsJSON, now).
		Scan(&role.ID, &role.Name, &role.Code, &desc, &role.IsSystem, &role.CreatedAt, &role.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique") {
			response.BadRequest(c, "Role with this code already exists")
			return
		}
		h.log.Error("Failed to create role", "error", err)
		response.InternalError(c, "Failed to create role")
		return
	}

	if desc.Valid {
		role.Description = &desc.String
	}
	role.Permissions = input.Permissions

	response.Created(c, role)
}

// GetRole returns a single role by ID
func (h *Handler) GetRole(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	roleID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid role ID")
		return
	}

	var role RoleResponse
	var desc sql.NullString
	var permsJSON []byte

	err = h.db.QueryRow(`
		SELECT id, name, code, description, is_system, permissions, created_at, updated_at
		FROM roles
		WHERE id = $1 AND tenant_id = $2
	`, roleID, tenantID).
		Scan(&role.ID, &role.Name, &role.Code, &desc, &role.IsSystem, &permsJSON, &role.CreatedAt, &role.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Role")
			return
		}
		h.log.Error("Failed to get role", "error", err)
		response.InternalError(c, "Failed to get role")
		return
	}

	if desc.Valid {
		role.Description = &desc.String
	}

	role.Permissions = make(RolePermissions)
	if len(permsJSON) > 0 {
		json.Unmarshal(permsJSON, &role.Permissions)
	}

	response.Success(c, role)
}

// UpdateRole updates an existing role and syncs permissions to assigned employees
func (h *Handler) UpdateRole(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	roleID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid role ID")
		return
	}

	// Check role exists and is not system
	var isSystem bool
	err = h.db.QueryRow(`SELECT is_system FROM roles WHERE id = $1 AND tenant_id = $2`, roleID, tenantID).Scan(&isSystem)
	if err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Role")
			return
		}
		h.log.Error("Failed to check role", "error", err)
		response.InternalError(c, "Failed to update role")
		return
	}

	if isSystem {
		response.BadRequest(c, "Cannot modify system roles")
		return
	}

	var input UpdateRoleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	now := time.Now()

	// Build dynamic update
	setClauses := []string{"updated_at = $3"}
	args := []interface{}{roleID, tenantID, now}
	argIdx := 4

	if input.Name != nil {
		setClauses = append(setClauses, "name = $"+string(rune('0'+argIdx)))
		args = append(args, strings.TrimSpace(*input.Name))
		argIdx++
	}

	if input.Description != nil {
		setClauses = append(setClauses, "description = $"+string(rune('0'+argIdx)))
		args = append(args, *input.Description)
		argIdx++
	}

	if input.Permissions != nil {
		permsJSON, err := json.Marshal(*input.Permissions)
		if err != nil {
			response.InternalError(c, "Failed to encode permissions")
			return
		}
		setClauses = append(setClauses, "permissions = $"+string(rune('0'+argIdx)))
		args = append(args, permsJSON)
		argIdx++
	}

	// Use a simpler approach: update with explicit fields
	// Re-do this with a transaction for clarity
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to update role")
		return
	}
	defer tx.Rollback()

	// Fetch current role data
	var currentName, currentCode string
	var currentDesc sql.NullString
	var currentPermsJSON []byte
	var currentIsSystem bool

	err = tx.QueryRow(`
		SELECT name, code, description, is_system, permissions
		FROM roles WHERE id = $1 AND tenant_id = $2
	`, roleID, tenantID).Scan(&currentName, &currentCode, &currentDesc, &currentIsSystem, &currentPermsJSON)
	if err != nil {
		response.NotFound(c, "Role")
		return
	}

	// Apply updates
	newName := currentName
	newDesc := currentDesc
	newPermsJSON := currentPermsJSON

	if input.Name != nil {
		newName = strings.TrimSpace(*input.Name)
	}
	if input.Description != nil {
		newDesc = sql.NullString{String: *input.Description, Valid: true}
	}
	if input.Permissions != nil {
		permsJSON, _ := json.Marshal(*input.Permissions)
		newPermsJSON = permsJSON
	}

	_, err = tx.Exec(`
		UPDATE roles SET name = $3, description = $4, permissions = $5, updated_at = $6
		WHERE id = $1 AND tenant_id = $2
	`, roleID, tenantID, newName, newDesc, newPermsJSON, now)
	if err != nil {
		h.log.Error("Failed to update role", "error", err)
		response.InternalError(c, "Failed to update role")
		return
	}

	// Auto-apply: sync permissions to all employees assigned to this role
	if input.Permissions != nil {
		err = h.syncRolePermissionsToEmployees(tx, tenantID, roleID, *input.Permissions, now)
		if err != nil {
			h.log.Error("Failed to sync role permissions to employees", "error", err)
			response.InternalError(c, "Failed to sync permissions")
			return
		}
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to update role")
		return
	}

	// Build response
	var role RoleResponse
	role.ID = roleID
	role.Name = newName
	role.Code = currentCode
	role.IsSystem = currentIsSystem
	role.CreatedAt = now
	role.UpdatedAt = now

	if newDesc.Valid {
		role.Description = &newDesc.String
	}

	role.Permissions = make(RolePermissions)
	if len(newPermsJSON) > 0 {
		json.Unmarshal(newPermsJSON, &role.Permissions)
	}

	response.Success(c, role)
}

// DeleteRole deletes a role and unlinks employees
func (h *Handler) DeleteRole(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	roleID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid role ID")
		return
	}

	// Check role exists and is not system
	var isSystem bool
	err = h.db.QueryRow(`SELECT is_system FROM roles WHERE id = $1 AND tenant_id = $2`, roleID, tenantID).Scan(&isSystem)
	if err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Role")
			return
		}
		h.log.Error("Failed to check role", "error", err)
		response.InternalError(c, "Failed to delete role")
		return
	}

	if isSystem {
		response.BadRequest(c, "Cannot delete system roles")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to delete role")
		return
	}
	defer tx.Rollback()

	// Unlink employees from this role
	_, err = tx.Exec(`UPDATE employees SET role_id = NULL WHERE role_id = $1 AND tenant_id = $2`, roleID, tenantID)
	if err != nil {
		h.log.Error("Failed to unlink employees from role", "error", err)
		response.InternalError(c, "Failed to delete role")
		return
	}

	// Delete the role
	_, err = tx.Exec(`DELETE FROM roles WHERE id = $1 AND tenant_id = $2`, roleID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete role", "error", err)
		response.InternalError(c, "Failed to delete role")
		return
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to delete role")
		return
	}

	response.NoContent(c)
}

// AssignRole assigns a role to an employee and applies the role's permissions
func (h *Handler) AssignRole(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	roleID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid role ID")
		return
	}

	var input struct {
		EmployeeID string `json:"employee_id" binding:"required"`
	}
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

	// Fetch role permissions
	var permsJSON []byte
	err = h.db.QueryRow(`SELECT permissions FROM roles WHERE id = $1 AND tenant_id = $2`, roleID, tenantID).Scan(&permsJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Role")
			return
		}
		h.log.Error("Failed to fetch role", "error", err)
		response.InternalError(c, "Failed to assign role")
		return
	}

	var perms RolePermissions
	if len(permsJSON) > 0 {
		json.Unmarshal(permsJSON, &perms)
	}

	now := time.Now()

	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to assign role")
		return
	}
	defer tx.Rollback()

	// Set role_id on employee
	result, err := tx.Exec(`
		UPDATE employees SET role_id = $1, updated_at = $2
		WHERE id = $3 AND tenant_id = $4 AND deleted_at IS NULL
	`, roleID, now, employeeID, tenantID)
	if err != nil {
		h.log.Error("Failed to assign role to employee", "error", err)
		response.InternalError(c, "Failed to assign role")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Employee")
		return
	}

	// Apply role permissions to employee
	if perms != nil {
		err = h.applyPermissionsToEmployee(tx, tenantID, employeeID, perms, now)
		if err != nil {
			h.log.Error("Failed to apply role permissions", "error", err)
			response.InternalError(c, "Failed to apply permissions")
			return
		}
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to assign role")
		return
	}

	response.Success(c, gin.H{"message": "Role assigned successfully"})
}

// UnassignRole removes a role from an employee
func (h *Handler) UnassignRole(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	roleID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid role ID")
		return
	}

	var input struct {
		EmployeeID string `json:"employee_id" binding:"required"`
	}
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

	now := time.Now()

	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to unassign role")
		return
	}
	defer tx.Rollback()

	// Unlink role from employee
	_, err = tx.Exec(`
		UPDATE employees SET role_id = NULL, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND role_id = $4 AND deleted_at IS NULL
	`, now, employeeID, tenantID, roleID)
	if err != nil {
		h.log.Error("Failed to unassign role", "error", err)
		response.InternalError(c, "Failed to unassign role")
		return
	}

	// Clear employee's module permissions
	_, err = tx.Exec(`DELETE FROM employee_module_permissions WHERE tenant_id = $1 AND employee_id = $2`, tenantID, employeeID)
	if err != nil {
		h.log.Error("Failed to clear employee permissions", "error", err)
		response.InternalError(c, "Failed to clear permissions")
		return
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to unassign role")
		return
	}

	response.Success(c, gin.H{"message": "Role unassigned successfully"})
}

// GetRoleEmployees returns all employees assigned to a role
func (h *Handler) GetRoleEmployees(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	roleID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid role ID")
		return
	}

	rows, err := h.db.Query(`
		SELECT id, TRIM(CONCAT(COALESCE(first_name, ''), ' ', COALESCE(last_name, ''))) AS full_name,
		       COALESCE(job_title, '') AS job_title, COALESCE(email, '') AS email
		FROM employees
		WHERE tenant_id = $1 AND role_id = $2 AND deleted_at IS NULL
		ORDER BY first_name ASC, last_name ASC
	`, tenantID, roleID)
	if err != nil {
		h.log.Error("Failed to list role employees", "error", err)
		response.InternalError(c, "Failed to list role employees")
		return
	}
	defer rows.Close()

	type EmpRow struct {
		ID       uuid.UUID `json:"id"`
		FullName string    `json:"full_name"`
		JobTitle string    `json:"job_title"`
		Email    string    `json:"email"`
	}
	employees := []EmpRow{}
	for rows.Next() {
		var e EmpRow
		if err := rows.Scan(&e.ID, &e.FullName, &e.JobTitle, &e.Email); err != nil {
			continue
		}
		employees = append(employees, e)
	}

	response.Success(c, employees)
}

// syncRolePermissionsToEmployees syncs role permissions to all employees with this role
func (h *Handler) syncRolePermissionsToEmployees(tx *sql.Tx, tenantID, roleID uuid.UUID, perms RolePermissions, now time.Time) error {
	// Find all employees with this role
	rows, err := tx.Query(`
		SELECT id FROM employees
		WHERE tenant_id = $1 AND role_id = $2 AND deleted_at IS NULL
	`, tenantID, roleID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var employeeIDs []uuid.UUID
	for rows.Next() {
		var empID uuid.UUID
		if err := rows.Scan(&empID); err != nil {
			return err
		}
		employeeIDs = append(employeeIDs, empID)
	}

	// Apply permissions to each employee
	for _, empID := range employeeIDs {
		if err := h.applyPermissionsToEmployee(tx, tenantID, empID, perms, now); err != nil {
			return err
		}
	}

	return nil
}

// applyPermissionsToEmployee replaces an employee's module permissions with the given set
func (h *Handler) applyPermissionsToEmployee(tx *sql.Tx, tenantID, employeeID uuid.UUID, perms RolePermissions, now time.Time) error {
	// Delete existing permissions
	_, err := tx.Exec(`DELETE FROM employee_module_permissions WHERE tenant_id = $1 AND employee_id = $2`, tenantID, employeeID)
	if err != nil {
		return err
	}

	// Insert new permissions
	for moduleID, perm := range perms {
		_, err = tx.Exec(`
			INSERT INTO employee_module_permissions (tenant_id, employee_id, module_id, can_create, can_read, can_update, can_delete, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
		`, tenantID, employeeID, moduleID, perm.CanCreate, perm.CanRead, perm.CanUpdate, perm.CanDelete, now)
		if err != nil {
			return err
		}
	}

	return nil
}
