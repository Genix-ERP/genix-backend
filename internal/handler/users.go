package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/infrastructure/email"
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
		       password_hash, invite_token_expires,
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
		var phone, avatar, passwordHash sql.NullString
		var lastLogin, inviteExpires sql.NullTime

		err := rows.Scan(
			&u.ID, &u.Email, &u.FirstName, &u.LastName,
			&phone, &avatar, &u.Language, &u.Timezone,
			&u.IsActive, &u.IsVerified, &lastLogin,
			&passwordHash, &inviteExpires,
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
		if inviteExpires.Valid {
			u.InviteTokenExpires = &inviteExpires.Time
		}
		u.FullName = u.FirstName + " " + u.LastName
		u.HasPassword = passwordHash.Valid && passwordHash.String != ""

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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// At least email or phone must be provided
	if input.Email == "" && input.Phone == "" {
		response.BadRequest(c, "Email or phone number is required")
		return
	}

	// Normalize phone
	if input.Phone != "" {
		input.Phone = normalizePhone(input.Phone)
	}

	// Check for duplicates
	var existingID uuid.UUID
	if input.Email != "" {
		err := h.db.QueryRow(
			"SELECT id FROM users WHERE tenant_id = $1 AND email = $2 AND deleted_at IS NULL",
			tenantID, input.Email,
		).Scan(&existingID)
		if err == nil {
			response.Conflict(c, "Email already exists")
			return
		}
	}
	if input.Phone != "" {
		err := h.db.QueryRow(
			"SELECT id FROM users WHERE tenant_id = $1 AND phone = $2 AND deleted_at IS NULL",
			tenantID, input.Phone,
		).Scan(&existingID)
		if err == nil {
			response.Conflict(c, "Phone number already exists")
			return
		}
	}

	// Hash password if provided
	var passwordHash string
	if input.Password != "" {
		var hashErr error
		passwordHash, hashErr = crypto.HashPassword(input.Password)
		if hashErr != nil {
			h.log.Error("Failed to hash password", "error", hashErr)
			response.InternalServerError(c, "")
			return
		}
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

	// Create user (password_hash can be empty for invite flow)
	userID := uuid.New()
	_, err := h.db.Exec(`
		INSERT INTO users (id, tenant_id, email, password_hash, first_name, last_name, phone, language, timezone, settings, is_active, is_verified)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, '{}', true, $10)
	`, userID, tenantID, input.Email, passwordHash, input.FirstName, input.LastName, input.Phone, language, timezone, passwordHash != "")

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

// credentialsTranslation holds translated credential message templates
type credentialsTranslation struct {
	SMSTemplate   string // %s = password
	EmailSubject  string // %s = tenant name
	EmailTitle    string
	EmailBody     string // %s = tenant name, %s = email, %s = password
	EmailWarning  string
}

var credentialsTranslations = map[string]credentialsTranslation{
	"uz": {
		SMSTemplate:  "Sizning Genix Admin paneliga kirish kodingiz: %s",
		EmailSubject: "%s - Kirish ma'lumotlari",
		EmailTitle:   "Kirish ma'lumotlari",
		EmailBody:    "Sizning <strong>%s</strong> tizimiga kirish ma'lumotlaringiz:",
		EmailWarning: "Iltimos, tizimga kirganingizdan so'ng parolni o'zgartiring.",
	},
	"en": {
		SMSTemplate:  "Your Genix Admin panel login code: %s",
		EmailSubject: "%s - Login Credentials",
		EmailTitle:   "Login Credentials",
		EmailBody:    "Your login credentials for <strong>%s</strong>:",
		EmailWarning: "Please change your password after logging in.",
	},
	"ru": {
		SMSTemplate:  "Ваш код для входа в панель Genix Admin: %s",
		EmailSubject: "%s - Данные для входа",
		EmailTitle:   "Данные для входа",
		EmailBody:    "Ваши данные для входа в <strong>%s</strong>:",
		EmailWarning: "Пожалуйста, смените пароль после входа в систему.",
	},
}

// getTenantLanguage reads the company language from tenant_settings
func (h *Handler) getTenantLanguage(tenantID string) string {
	var settingsJSON []byte
	err := h.db.QueryRow("SELECT settings FROM tenant_settings WHERE tenant_id = $1", tenantID).Scan(&settingsJSON)
	if err != nil {
		return "en"
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(settingsJSON, &settings); err != nil {
		return "en"
	}
	if general, ok := settings["general"].(map[string]interface{}); ok {
		if loc, ok := general["localization"].(map[string]interface{}); ok {
			if lang, ok := loc["language"].(string); ok && lang != "" {
				return lang
			}
		}
	}
	return "en"
}

// SendCredentials generates a new password, updates the user, and sends credentials via email
func (h *Handler) SendCredentials(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.BadRequest(c, "Tenant ID required")
		return
	}

	var input struct {
		Email    string `json:"email"`
		Password string `json:"password" binding:"required"`
		Method   string `json:"method"` // "email" or "sms"
		Phone    string `json:"phone"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	if input.Method == "" {
		input.Method = "email"
	}

	// Validate: need email for email method, phone for sms method
	if input.Method == "email" && input.Email == "" {
		response.BadRequest(c, "Email is required for email delivery")
		return
	}
	if input.Method == "sms" && input.Phone == "" {
		response.BadRequest(c, "Phone number is required for SMS delivery")
		return
	}

	// Hash and update the user's password
	passwordHash, err := crypto.HashPassword(input.Password)
	if err != nil {
		h.log.Error("Failed to hash password", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Find and update user by email or phone
	var result sql.Result
	if input.Email != "" {
		result, err = h.db.Exec(
			"UPDATE users SET password_hash = $1, updated_at = NOW() WHERE tenant_id = $2 AND email = $3 AND deleted_at IS NULL",
			passwordHash, tenantID, input.Email,
		)
	} else {
		phone := normalizePhone(input.Phone)
		result, err = h.db.Exec(
			"UPDATE users SET password_hash = $1, updated_at = NOW() WHERE tenant_id = $2 AND phone = $3 AND deleted_at IS NULL",
			passwordHash, tenantID, phone,
		)
	}
	if err != nil {
		h.log.Error("Failed to update user password", "error", err)
		response.InternalServerError(c, "")
		return
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "User not found")
		return
	}

	// Get tenant language and name
	lang := h.getTenantLanguage(tenantID.String())
	trans, ok2 := credentialsTranslations[lang]
	if !ok2 {
		trans = credentialsTranslations["en"]
	}

	var tenantName string
	_ = h.db.QueryRow("SELECT name FROM tenants WHERE id = $1", tenantID).Scan(&tenantName)
	if tenantName == "" {
		tenantName = "GenixERP"
	}

	if input.Method == "sms" {
		if input.Phone == "" {
			response.BadRequest(c, "Phone number is required for SMS")
			return
		}
		smsMessage := fmt.Sprintf(trans.SMSTemplate, input.Password)
		if err := h.smsService.Send(input.Phone, smsMessage); err != nil {
			h.log.Error("Failed to send credentials SMS", "error", err)
			response.InternalServerError(c, "Failed to send SMS")
			return
		}
		response.Success(c, gin.H{"message": "Credentials sent via SMS"})
		return
	}

	// Send via email
	err = h.emailService.Send(&email.Email{
		To:      []string{input.Email},
		Subject: fmt.Sprintf(trans.EmailSubject, tenantName),
		Body: fmt.Sprintf(`<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"></head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
  <div style="background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%); padding: 30px; border-radius: 10px 10px 0 0; text-align: center;">
    <h1 style="color: white; margin: 0;">GenixERP</h1>
  </div>
  <div style="background: #fff; padding: 30px; border: 1px solid #e0e0e0; border-top: none; border-radius: 0 0 10px 10px;">
    <h2 style="margin-top: 0;">%s</h2>
    <p>%s</p>
    <div style="background: #f5f5f5; padding: 20px; border-radius: 8px; margin: 20px 0;">
      <p style="margin: 5px 0;"><strong>Email:</strong> %s</p>
      <p style="margin: 5px 0;"><strong>Password:</strong> <code style="font-size: 18px; letter-spacing: 2px;">%s</code></p>
    </div>
    <p style="color: #666; font-size: 14px;">%s</p>
  </div>
</body>
</html>`, trans.EmailTitle, fmt.Sprintf(trans.EmailBody, tenantName), input.Email, input.Password, trans.EmailWarning),
		IsHTML: true,
	})
	if err != nil {
		h.log.Error("Failed to send credentials email", "error", err)
		response.InternalServerError(c, "Failed to send email")
		return
	}

	response.Success(c, gin.H{"message": "Credentials sent via email"})
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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

	// Hard delete user to free up the email for reuse
	// First delete related records
	h.db.Exec("DELETE FROM user_roles WHERE user_id = $1", userID)
	h.db.Exec("DELETE FROM refresh_tokens WHERE user_id = $1", userID)

	result, err := h.db.Exec(
		"DELETE FROM users WHERE id = $1 AND tenant_id = $2",
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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

// ListAllSystemUsers lists tenant owners across all tenants (for system admin only)
// This shows one owner per tenant - the primary user who manages the subscription
func (h *Handler) ListAllSystemUsers(c *gin.Context) {
	page := getIntParam(c, "page", 1)
	limit := getIntParam(c, "limit", 50)
	search := c.Query("search")
	statusFilter := c.Query("status")
	_ = c.Query("role") // roleFilter for future use

	pagination := entity.NewPagination(page, limit)

	// Count total tenant owners (users with 'owner' role or is_system_admin=true for backwards compatibility)
	countQuery := `SELECT COUNT(DISTINCT u.id) FROM users u
		INNER JOIN tenants t ON u.tenant_id = t.id
		LEFT JOIN user_roles ur ON u.id = ur.user_id
		LEFT JOIN roles r ON ur.role_id = r.id
		WHERE u.deleted_at IS NULL
		AND (u.is_system_admin = true OR r.code = 'owner')`
	args := []interface{}{}
	argIdx := 1

	if search != "" {
		countQuery += " AND (u.first_name ILIKE $" + itoa(argIdx) + " OR u.last_name ILIKE $" + itoa(argIdx) + " OR u.email ILIKE $" + itoa(argIdx) + " OR t.name ILIKE $" + itoa(argIdx) + ")"
		args = append(args, "%"+search+"%")
		argIdx++
	}

	if statusFilter == "blocked" {
		countQuery += " AND u.is_active = false"
	} else if statusFilter == "active" {
		countQuery += " AND u.is_active = true"
	}

	var total int
	h.db.QueryRow(countQuery, args...).Scan(&total)
	pagination.Calculate(total)

	// Query tenant owners with tenant info and user count
	// Find users with 'owner' role or is_system_admin=true for backwards compatibility
	query := `
		SELECT DISTINCT u.id, u.email, u.first_name, u.last_name, u.phone, u.avatar_url,
		       u.language, u.timezone, u.is_active, u.is_verified, u.last_login_at,
		       u.created_at, u.updated_at,
		       t.id as tenant_id, t.name as tenant_name, t.code as tenant_code,
		       t.subscription_plan, t.subscription_status,
		       COALESCE(u.is_system_admin, false) as is_system_admin,
		       (SELECT COUNT(*) FROM users u2 WHERE u2.tenant_id = t.id AND u2.deleted_at IS NULL) as user_count
		FROM users u
		INNER JOIN tenants t ON u.tenant_id = t.id
		LEFT JOIN user_roles ur ON u.id = ur.user_id
		LEFT JOIN roles r ON ur.role_id = r.id
		WHERE u.deleted_at IS NULL
		AND (u.is_system_admin = true OR r.code = 'owner')
	`

	// Reset args for main query
	args = []interface{}{}
	argIdx = 1

	if search != "" {
		query += " AND (u.first_name ILIKE $" + itoa(argIdx) + " OR u.last_name ILIKE $" + itoa(argIdx) + " OR u.email ILIKE $" + itoa(argIdx) + " OR t.name ILIKE $" + itoa(argIdx) + ")"
		args = append(args, "%"+search+"%")
		argIdx++
	}

	if statusFilter == "blocked" {
		query += " AND u.is_active = false"
	} else if statusFilter == "active" {
		query += " AND u.is_active = true"
	}

	query += " ORDER BY u.created_at DESC LIMIT $" + itoa(argIdx) + " OFFSET $" + itoa(argIdx+1)
	args = append(args, pagination.Limit, pagination.Offset())

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list all system users", "error", err)
		response.InternalServerError(c, "")
		return
	}
	defer rows.Close()

	type TenantOwnerResponse struct {
		ID            uuid.UUID  `json:"id"`
		Email         string     `json:"email"`
		FirstName     string     `json:"first_name"`
		LastName      string     `json:"last_name"`
		FullName      string     `json:"full_name"`
		Phone         *string    `json:"phone,omitempty"`
		AvatarURL     *string    `json:"avatar_url,omitempty"`
		Language      string     `json:"language"`
		Timezone      string     `json:"timezone"`
		IsActive      bool       `json:"is_active"`
		IsVerified    bool       `json:"is_verified"`
		IsSystemAdmin bool       `json:"is_system_admin"`
		LastLoginAt   *time.Time `json:"last_login_at,omitempty"`
		CreatedAt     time.Time  `json:"created_at"`
		UpdatedAt     time.Time  `json:"updated_at"`
		// Tenant info
		TenantID   *uuid.UUID `json:"tenant_id,omitempty"`
		TenantName *string    `json:"tenant_name,omitempty"`
		TenantCode *string    `json:"tenant_code,omitempty"`
		UserCount  int        `json:"user_count"` // Number of users in tenant
		// Subscription info from tenant
		SubscriptionPlan   string `json:"subscription_plan"`
		SubscriptionStatus string `json:"subscription_status"`
		// Frontend compatibility
		Status string `json:"status"`
		Role   string `json:"role"`
	}

	var users []TenantOwnerResponse
	for rows.Next() {
		var u TenantOwnerResponse
		var phone, avatar sql.NullString
		var lastLogin sql.NullTime
		var tenantID sql.NullString
		var tenantName, tenantCode sql.NullString
		var subscriptionPlan, subscriptionStatus sql.NullString

		err := rows.Scan(
			&u.ID, &u.Email, &u.FirstName, &u.LastName,
			&phone, &avatar, &u.Language, &u.Timezone,
			&u.IsActive, &u.IsVerified, &lastLogin,
			&u.CreatedAt, &u.UpdatedAt,
			&tenantID, &tenantName, &tenantCode,
			&subscriptionPlan, &subscriptionStatus,
			&u.IsSystemAdmin, &u.UserCount,
		)
		if err != nil {
			h.log.Error("Failed to scan tenant owner", "error", err)
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
		if tenantID.Valid {
			if parsed, err := uuid.Parse(tenantID.String); err == nil {
				u.TenantID = &parsed
			}
		}
		if tenantName.Valid {
			u.TenantName = &tenantName.String
		}
		if tenantCode.Valid {
			u.TenantCode = &tenantCode.String
		}
		if subscriptionPlan.Valid {
			u.SubscriptionPlan = subscriptionPlan.String
		} else {
			u.SubscriptionPlan = "free"
		}
		if subscriptionStatus.Valid {
			u.SubscriptionStatus = subscriptionStatus.String
		} else {
			u.SubscriptionStatus = "trial"
		}

		u.FullName = u.FirstName + " " + u.LastName

		// Set status for frontend (based on user account status)
		if !u.IsActive {
			u.Status = "blocked"
		} else if u.IsVerified {
			u.Status = "active"
		} else {
			u.Status = "trial"
		}

		// Role is always owner for tenant owners
		u.Role = "owner"

		users = append(users, u)
	}

	response.SuccessWithMeta(c, users, pagination)
}

// DeleteSystemUser soft-deletes a user (for system admin only)
// This marks the user as deleted without removing data
func (h *Handler) DeleteSystemUser(c *gin.Context) {
	userID := c.Param("id")
	if userID == "" {
		response.BadRequest(c, "User ID is required")
		return
	}

	parsedID, err := uuid.Parse(userID)
	if err != nil {
		response.BadRequest(c, "Invalid user ID format")
		return
	}

	// Get the user's email before deletion for the response
	var email string
	err = h.db.QueryRow("SELECT email FROM users WHERE id = $1 AND deleted_at IS NULL", parsedID).Scan(&email)
	if err != nil {
		response.NotFound(c, "User not found")
		return
	}

	// Soft delete the user (set deleted_at)
	now := time.Now()
	result, err := h.db.Exec(`
		UPDATE users SET deleted_at = $1, updated_at = $1, is_active = false
		WHERE id = $2 AND deleted_at IS NULL
	`, now, parsedID)
	if err != nil {
		h.log.Error("Failed to delete user", "error", err, "user_id", parsedID)
		response.InternalError(c, "Failed to delete user")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "User not found or already deleted")
		return
	}

	h.log.Info("System admin deleted user", "deleted_user_id", parsedID, "email", email)

	response.Success(c, map[string]interface{}{
		"message": "User deleted successfully",
		"email":   email,
	})
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
