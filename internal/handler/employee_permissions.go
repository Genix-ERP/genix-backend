package handler

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ModulePermission represents a single module permission entry
type ModulePermission struct {
	ID        uuid.UUID `json:"id"`
	ModuleID  string    `json:"module_id"`
	CanCreate bool      `json:"can_create"`
	CanRead   bool      `json:"can_read"`
	CanUpdate bool      `json:"can_update"`
	CanDelete bool      `json:"can_delete"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// EmployeePermissionsResponse represents the full permissions for an employee
type EmployeePermissionsResponse struct {
	EmployeeID  uuid.UUID                    `json:"employee_id"`
	RoleID      *uuid.UUID                   `json:"role_id,omitempty"`
	RoleName    string                       `json:"role_name,omitempty"`
	Permissions map[string]*ModulePermission `json:"permissions"`
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
	Permissions []UpdateModulePermissionInput `json:"permissions" binding:"required,dive"`
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

	// Verify employee exists
	var exists bool
	err = h.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)
	`, employeeID, tenantID).Scan(&exists)
	if err != nil || !exists {
		response.NotFound(c, "Employee")
		return
	}

	// Fetch role info separately to avoid scan type issues
	var roleID *uuid.UUID
	var roleName string
	var tmpRoleIDStr sql.NullString
	var tmpRoleName sql.NullString
	_ = h.db.QueryRow(`
		SELECT e.role_id::text, COALESCE(r.name, '') as role_name
		FROM employees e
		LEFT JOIN roles r ON r.id = e.role_id AND r.deleted_at IS NULL
		WHERE e.id = $1 AND e.tenant_id = $2 AND e.deleted_at IS NULL
	`, employeeID, tenantID).Scan(&tmpRoleIDStr, &tmpRoleName)
	if tmpRoleIDStr.Valid && tmpRoleIDStr.String != "" {
		if parsed, parseErr := uuid.Parse(tmpRoleIDStr.String); parseErr == nil {
			roleID = &parsed
			roleName = tmpRoleName.String
		}
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
		RoleID:      roleID,
		RoleName:    roleName,
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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

	// Invalidate permission cache for the linked user
	var linkedUserID uuid.UUID
	if scanErr := h.db.QueryRow(
		"SELECT id FROM users WHERE tenant_id = $1 AND employee_id = $2 AND deleted_at IS NULL",
		tenantID, employeeID,
	).Scan(&linkedUserID); scanErr == nil {
		h.perm.InvalidatePermissionCache(c.Request.Context(), tenantID.String(), linkedUserID.String())
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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

	// If employee has a role, sync permissions back to the role and propagate to all role members
	var empRoleID uuid.NullUUID
	_ = tx.QueryRow(`SELECT role_id FROM employees WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, employeeID, tenantID).Scan(&empRoleID)
	if empRoleID.Valid {
		// Build RolePermissions from the input
		rolePerms := make(RolePermissions)
		for _, p := range input.Permissions {
			rolePerms[p.ModuleID] = RoleModulePerm{
				CanCreate: p.CanCreate,
				CanRead:   p.CanRead,
				CanUpdate: p.CanUpdate,
				CanDelete: p.CanDelete,
			}
		}
		// Update the role's permissions JSONB
		permsJSON, _ := json.Marshal(rolePerms)
		_, err = tx.Exec(`UPDATE roles SET permissions = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4`,
			permsJSON, now, empRoleID.UUID, tenantID)
		if err != nil {
			h.log.Error("Failed to update role permissions", "error", err)
			// Don't fail the whole operation — employee permissions are already saved
		} else {
			// Sync to all other employees with the same role
			if syncErr := h.syncRolePermissionsToEmployees(tx, tenantID, empRoleID.UUID, rolePerms, now); syncErr != nil {
				h.log.Error("Failed to sync role permissions to employees", "error", syncErr)
			}
		}
	}

	// Commit transaction
	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to update permissions")
		return
	}

	// Invalidate permission cache for the linked user
	var linkedUserID uuid.UUID
	if scanErr := h.db.QueryRow(
		"SELECT id FROM users WHERE tenant_id = $1 AND employee_id = $2 AND deleted_at IS NULL",
		tenantID, employeeID,
	).Scan(&linkedUserID); scanErr == nil {
		h.perm.InvalidatePermissionCache(c.Request.Context(), tenantID.String(), linkedUserID.String())
	}

	// Also invalidate cache for all other employees with the same role
	if empRoleID.Valid {
		userRows, _ := h.db.Query(`
			SELECT u.id FROM users u
			JOIN employees e ON e.id = u.employee_id
			WHERE e.tenant_id = $1 AND e.role_id = $2 AND e.deleted_at IS NULL AND u.deleted_at IS NULL
		`, tenantID, empRoleID.UUID)
		if userRows != nil {
			defer userRows.Close()
			for userRows.Next() {
				var uid uuid.UUID
				if userRows.Scan(&uid) == nil {
					h.perm.InvalidatePermissionCache(c.Request.Context(), tenantID.String(), uid.String())
				}
			}
		}
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
			"is_admin":    true,
			"permissions": nil,
		})
		return
	}

	// If no linked employee, return empty permissions
	if !employeeID.Valid {
		response.Success(c, gin.H{
			"is_admin":    false,
			"permissions": map[string]interface{}{},
		})
		return
	}

	empID, err := uuid.Parse(employeeID.String)
	if err != nil {
		response.Success(c, gin.H{
			"is_admin":    false,
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

// GetCurrentUserOrganizations returns full organization details for the current user's employee
// GetCurrentUserOrganizations — DO NOT PAGINATE THIS ENDPOINT.
//
// It is the org-switcher bootstrap, and the client resolves its persisted
// selection by scanning the full array:
//
//	selected = organizations.where((org) => org.id == savedOrgId).firstOrNull;
//	selected ??= organizations.firstOrNull;
//
// If the saved organization is not on page 1, firstOrNull silently reassigns
// the user to a DIFFERENT company and persists that choice — every subsequent
// request then carries the wrong X-Organization-Id. Paginating here is a
// data-integrity bug, not a payload win.
//
// The list is also the smallest in the module: capped by the tenant's own
// organization count for admins, and by the user's assignments otherwise.
// If paging is ever genuinely needed, the unpaged full array must remain the
// DEFAULT with paging strictly opt-in — the reverse of the house rule, for the
// reason above.
func (h *Handler) GetCurrentUserOrganizations(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, ok := middleware.GetUserID(c)
	if !ok || userID == uuid.Nil {
		response.Unauthorized(c, "User not found")
		return
	}

	// Get user role and employee link
	var role string
	var employeeID sql.NullString
	err := h.db.QueryRow(`
		SELECT role, employee_id FROM users WHERE id = $1 AND tenant_id = $2
	`, userID, tenantID).Scan(&role, &employeeID)
	if err != nil {
		response.InternalError(c, "Failed to get user")
		return
	}

	var query string
	var args []interface{}

	// Admins get all organizations
	if role == "site_admin" || role == "owner" || role == "system_admin" {
		query = `
			SELECT id, tenant_id, parent_id, code, name, type, tax_id, registration_number,
			       address, contact_info, country, currency, accounting_standard, logo_url,
			       settings, is_active, created_at, updated_at,
			       oked, bank_account, bank_mfo, bank_name, COALESCE(is_vat_payer, false),
			       tax_regime, activity_status, business_group, intercompany_relations,
			       director_name, director_phone, legal_address, notes
			FROM organizations WHERE tenant_id = $1 AND deleted_at IS NULL
			ORDER BY name ASC
		`
		args = []interface{}{tenantID}
	} else {
		// Non-admin: get only assigned organizations
		if !employeeID.Valid {
			response.Success(c, []Organization{})
			return
		}
		query = `
			SELECT o.id, o.tenant_id, o.parent_id, o.code, o.name, o.type, o.tax_id, o.registration_number,
			       o.address, o.contact_info, o.country, o.currency, o.accounting_standard, o.logo_url,
			       o.settings, o.is_active, o.created_at, o.updated_at,
			       o.oked, o.bank_account, o.bank_mfo, o.bank_name, COALESCE(o.is_vat_payer, false),
			       o.tax_regime, o.activity_status, o.business_group, o.intercompany_relations,
			       o.director_name, o.director_phone, o.legal_address, o.notes
			FROM organizations o
			INNER JOIN employee_organizations eo ON eo.organization_id = o.id AND eo.tenant_id = o.tenant_id
			WHERE o.tenant_id = $1 AND eo.employee_id = $2 AND o.deleted_at IS NULL
			ORDER BY o.name ASC
		`
		args = []interface{}{tenantID, employeeID.String}
	}

	rows, err := h.db.Query(query, args...)
	if err != nil {
		response.InternalError(c, "Failed to fetch organizations")
		return
	}
	defer rows.Close()

	var organizations []Organization
	for rows.Next() {
		var org Organization
		var addressJSON, contactInfoJSON, settingsJSON []byte

		if err := rows.Scan(
			&org.ID, &org.TenantID, &org.ParentID, &org.Code, &org.Name, &org.Type,
			&org.TaxID, &org.RegistrationNumber, &addressJSON, &contactInfoJSON,
			&org.Country, &org.Currency, &org.AccountingStandard, &org.LogoURL,
			&settingsJSON, &org.IsActive, &org.CreatedAt, &org.UpdatedAt,
			&org.OKED, &org.BankAccount, &org.BankMFO, &org.BankName, &org.IsVATPayer,
			&org.TaxRegime, &org.ActivityStatus, &org.BusinessGroup, &org.IntercompanyRelations,
			&org.DirectorName, &org.DirectorPhone, &org.LegalAddress, &org.Notes,
		); err != nil {
			h.log.Error("Failed to scan organization", "error", err)
			continue
		}

		if len(addressJSON) > 0 {
			json.Unmarshal(addressJSON, &org.Address)
		}
		if len(contactInfoJSON) > 0 {
			json.Unmarshal(contactInfoJSON, &org.ContactInfo)
		}
		if len(settingsJSON) > 0 {
			json.Unmarshal(settingsJSON, &org.Settings)
		}

		organizations = append(organizations, org)
	}

	if organizations == nil {
		organizations = []Organization{}
	}

	response.Success(c, organizations)
}
