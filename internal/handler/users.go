package handler

import (
	"database/sql"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/crypto"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListUsers lists all users for the tenant
func (h *Handler) ListUsers(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.BadRequest(c, "Tenant ID required")
		return
	}

	page := getIntParam(c, "page", 1)
	limit := getIntParam(c, "limit", 20)
	search := c.Query("search")

	pagination := entity.NewPagination(page, limit)

	// Count total
	var total int
	countQuery := "SELECT COUNT(*) FROM users WHERE tenant_id = $1 AND deleted_at IS NULL"
	args := []interface{}{tenantID}

	if search != "" {
		countQuery += " AND (first_name ILIKE $2 OR last_name ILIKE $2 OR email ILIKE $2)"
		args = append(args, "%"+search+"%")
	}

	h.db.QueryRow(countQuery, args...).Scan(&total)
	pagination.Calculate(total)

	// Query users
	query := `
		SELECT id, email, first_name, last_name, phone, avatar_url,
		       language, timezone, is_active, is_verified, last_login_at,
		       created_at, updated_at
		FROM users
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`
	if search != "" {
		query += " AND (first_name ILIKE $2 OR last_name ILIKE $2 OR email ILIKE $2)"
	}
	query += " ORDER BY created_at DESC LIMIT $" + itoa(len(args)+1) + " OFFSET $" + itoa(len(args)+2)
	args = append(args, pagination.Limit, pagination.Offset())

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list users", "error", err)
		response.InternalServerError(c, "")
		return
	}
	defer rows.Close()

	var users []entity.UserResponse
	for rows.Next() {
		var u entity.UserResponse
		var phone, avatar sql.NullString
		var lastLogin sql.NullTime

		err := rows.Scan(
			&u.ID, &u.Email, &u.FirstName, &u.LastName,
			&phone, &avatar, &u.Language, &u.Timezone,
			&u.IsActive, &u.IsVerified, &lastLogin,
			&u.CreatedAt, &u.UpdatedAt,
		)
		if err != nil {
			continue
		}

		if phone.Valid {
			u.Phone = &phone.String
		}
		if avatar.Valid {
			u.AvatarURL = &avatar.String
		}
		if lastLogin.Valid {
			u.LastLoginAt = &lastLogin.Time
		}
		u.FullName = u.FirstName + " " + u.LastName

		users = append(users, u)
	}

	response.SuccessWithMeta(c, users, pagination)
}

// CreateUser creates a new user
func (h *Handler) CreateUser(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.BadRequest(c, "Tenant ID required")
		return
	}

	var input entity.CreateUserInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Check if email exists
	var existingID uuid.UUID
	err := h.db.QueryRow(
		"SELECT id FROM users WHERE tenant_id = $1 AND email = $2 AND deleted_at IS NULL",
		tenantID, input.Email,
	).Scan(&existingID)
	if err == nil {
		response.Conflict(c, "Email already exists")
		return
	}

	// Hash password
	passwordHash, err := crypto.HashPassword(input.Password)
	if err != nil {
		h.log.Error("Failed to hash password", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Set defaults
	language := input.Language
	if language == "" {
		language = "en"
	}
	timezone := input.Timezone
	if timezone == "" {
		timezone = "UTC"
	}

	// Create user
	userID := uuid.New()
	_, err = h.db.Exec(`
		INSERT INTO users (id, tenant_id, email, password_hash, first_name, last_name, phone, language, timezone, settings, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, '{}', true)
	`, userID, tenantID, input.Email, passwordHash, input.FirstName, input.LastName, input.Phone, language, timezone)

	if err != nil {
		h.log.Error("Failed to create user", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Assign roles if provided
	for _, roleIDStr := range input.RoleIDs {
		roleID, err := uuid.Parse(roleIDStr)
		if err != nil {
			continue
		}
		h.db.Exec("INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)", userID, roleID)
	}

	response.Created(c, gin.H{"id": userID})
}

// GetUser gets a user by ID
func (h *Handler) GetUser(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.BadRequest(c, "Tenant ID required")
		return
	}

	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid user ID")
		return
	}

	var u entity.UserResponse
	var phone, avatar sql.NullString
	var lastLogin sql.NullTime

	err = h.db.QueryRow(`
		SELECT id, email, first_name, last_name, phone, avatar_url,
		       language, timezone, is_active, is_verified, last_login_at,
		       created_at, updated_at
		FROM users
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, userID, tenantID).Scan(
		&u.ID, &u.Email, &u.FirstName, &u.LastName,
		&phone, &avatar, &u.Language, &u.Timezone,
		&u.IsActive, &u.IsVerified, &lastLogin,
		&u.CreatedAt, &u.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "User")
		return
	}
	if err != nil {
		h.log.Error("Failed to get user", "error", err)
		response.InternalServerError(c, "")
		return
	}

	if phone.Valid {
		u.Phone = &phone.String
	}
	if avatar.Valid {
		u.AvatarURL = &avatar.String
	}
	if lastLogin.Valid {
		u.LastLoginAt = &lastLogin.Time
	}
	u.FullName = u.FirstName + " " + u.LastName

	// Load roles
	rows, _ := h.db.Query(`
		SELECT r.id, r.name, r.code
		FROM roles r
		JOIN user_roles ur ON r.id = ur.role_id
		WHERE ur.user_id = $1
	`, userID)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var role entity.Role
			rows.Scan(&role.ID, &role.Name, &role.Code)
			u.Roles = append(u.Roles, role)
		}
	}

	response.Success(c, u)
}

// UpdateUser updates a user
func (h *Handler) UpdateUser(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.BadRequest(c, "Tenant ID required")
		return
	}

	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid user ID")
		return
	}

	var input entity.UpdateUserInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Verify user exists
	var existingID uuid.UUID
	err = h.db.QueryRow(
		"SELECT id FROM users WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL",
		userID, tenantID,
	).Scan(&existingID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "User")
		return
	}

	// Build and execute update
	_, err = h.db.Exec(`
		UPDATE users SET
			first_name = COALESCE($1, first_name),
			last_name = COALESCE($2, last_name),
			phone = COALESCE($3, phone),
			language = COALESCE($4, language),
			timezone = COALESCE($5, timezone),
			is_active = COALESCE($6, is_active),
			updated_at = NOW()
		WHERE id = $7 AND tenant_id = $8
	`, input.FirstName, input.LastName, input.Phone, input.Language, input.Timezone, input.IsActive, userID, tenantID)

	if err != nil {
		h.log.Error("Failed to update user", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Update roles if provided
	if len(input.RoleIDs) > 0 {
		h.db.Exec("DELETE FROM user_roles WHERE user_id = $1", userID)
		for _, roleIDStr := range input.RoleIDs {
			roleID, err := uuid.Parse(roleIDStr)
			if err != nil {
				continue
			}
			h.db.Exec("INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)", userID, roleID)
		}
	}

	h.GetUser(c)
}

// DeleteUser deletes a user (soft delete)
func (h *Handler) DeleteUser(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.BadRequest(c, "Tenant ID required")
		return
	}

	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid user ID")
		return
	}

	// Don't allow deleting self
	currentUserID, _ := middleware.GetUserID(c)
	if userID == currentUserID {
		response.BadRequest(c, "Cannot delete your own account")
		return
	}

	result, err := h.db.Exec(
		"UPDATE users SET deleted_at = NOW() WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL",
		userID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to delete user", "error", err)
		response.InternalServerError(c, "")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "User")
		return
	}

	response.NoContent(c)
}

// AssignRoles assigns roles to a user
func (h *Handler) AssignRoles(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.BadRequest(c, "Tenant ID required")
		return
	}

	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid user ID")
		return
	}

	var input struct {
		RoleIDs []string `json:"role_ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Verify user exists
	var existingID uuid.UUID
	err = h.db.QueryRow(
		"SELECT id FROM users WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL",
		userID, tenantID,
	).Scan(&existingID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "User")
		return
	}

	// Remove existing roles and assign new ones
	h.db.Exec("DELETE FROM user_roles WHERE user_id = $1", userID)
	for _, roleIDStr := range input.RoleIDs {
		roleID, err := uuid.Parse(roleIDStr)
		if err != nil {
			continue
		}
		h.db.Exec("INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)", userID, roleID)
	}

	response.Success(c, gin.H{"message": "Roles assigned successfully"})
}

// Helper functions
func getIntParam(c *gin.Context, name string, defaultVal int) int {
	val := c.Query(name)
	if val == "" {
		return defaultVal
	}
	result := 0
	for _, r := range val {
		if r >= '0' && r <= '9' {
			result = result*10 + int(r-'0')
		}
	}
	if result == 0 {
		return defaultVal
	}
	return result
}
