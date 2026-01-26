package handler

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/crypto"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// LoginInput represents login request
type LoginInput struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
	TenantID string `json:"tenant_id,omitempty"`
}

// RegisterInput represents registration request
type RegisterInput struct {
	TenantCode string `json:"tenant_code" binding:"required,min=2,max=50"`
	TenantName string `json:"tenant_name" binding:"required,min=2,max=255"`
	Email      string `json:"email" binding:"required,email"`
	Password   string `json:"password" binding:"required,min=8"`
	FirstName  string `json:"first_name" binding:"required,min=1,max=100"`
	LastName   string `json:"last_name" binding:"required,min=1,max=100"`
}

// RefreshTokenInput represents refresh token request
type RefreshTokenInput struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// ForgotPasswordInput represents forgot password request
type ForgotPasswordInput struct {
	Email string `json:"email" binding:"required,email"`
}

// ResetPasswordInput represents reset password request
type ResetPasswordInput struct {
	Token       string `json:"token" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=8"`
}

// VerifyEmailInput represents email verification request
type VerifyEmailInput struct {
	Token string `json:"token" binding:"required"`
}

// SendInviteInput represents send invitation request
type SendInviteInput struct {
	UserID string `json:"user_id" binding:"required,uuid"`
}

// AcceptInviteInput represents accept invitation request
type AcceptInviteInput struct {
	Token    string `json:"token" binding:"required"`
	Password string `json:"password" binding:"required,min=8"`
}

// ValidateInviteResponse represents the response when validating an invite token
type ValidateInviteResponse struct {
	Valid      bool   `json:"valid"`
	Email      string `json:"email,omitempty"`
	FirstName  string `json:"first_name,omitempty"`
	LastName   string `json:"last_name,omitempty"`
	TenantName string `json:"tenant_name,omitempty"`
	ExpiresAt  string `json:"expires_at,omitempty"`
}

// TenantInfo represents basic tenant information
type TenantInfo struct {
	ID   uuid.UUID `json:"id"`
	Code string    `json:"code"`
	Name string    `json:"name"`
}

// AuthResponse represents authentication response
type AuthResponse struct {
	User         *entity.UserResponse `json:"user"`
	Tenant       *TenantInfo          `json:"tenant,omitempty"`
	AccessToken  string               `json:"access_token"`
	RefreshToken string               `json:"refresh_token"`
	ExpiresAt    time.Time            `json:"expires_at"`
	TokenType    string               `json:"token_type"`
}

// Register handles user registration
func (h *Handler) Register(c *gin.Context) {
	var input RegisterInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to begin transaction", "error", err)
		response.InternalServerError(c, "")
		return
	}
	defer tx.Rollback()

	// Check if email already exists globally (across all tenants)
	var existingUserID uuid.UUID
	err = tx.QueryRow("SELECT id FROM users WHERE email = $1 AND deleted_at IS NULL", input.Email).Scan(&existingUserID)
	if err == nil {
		response.Conflict(c, "Email already registered. Please use a different email or login to your existing account.")
		return
	}
	if err != sql.ErrNoRows {
		h.log.Error("Failed to check email", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Check if tenant code exists
	var existingTenantID uuid.UUID
	err = tx.QueryRow("SELECT id FROM tenants WHERE code = $1", input.TenantCode).Scan(&existingTenantID)
	if err == nil {
		response.Conflict(c, "Tenant code already exists")
		return
	}
	if err != sql.ErrNoRows {
		h.log.Error("Failed to check tenant", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Create tenant
	tenantID := uuid.New()
	defaultSettings, _ := json.Marshal(map[string]interface{}{
		"locale": map[string]string{
			"language":         "en",
			"timezone":         "UTC",
			"date_format":      "YYYY-MM-DD",
			"default_currency": "USD",
		},
	})

	_, err = tx.Exec(`
		INSERT INTO tenants (id, code, name, settings, subscription_plan, subscription_status)
		VALUES ($1, $2, $3, $4, 'free', 'active')
	`, tenantID, input.TenantCode, input.TenantName, defaultSettings)
	if err != nil {
		h.log.Error("Failed to create tenant", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Hash password
	passwordHash, err := crypto.HashPassword(input.Password)
	if err != nil {
		h.log.Error("Failed to hash password", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Create user (email uniqueness already checked globally above)
	userID := uuid.New()
	now := time.Now()
	defaultUserSettings, _ := json.Marshal(map[string]interface{}{})

	_, err = tx.Exec(`
		INSERT INTO users (id, tenant_id, email, password_hash, first_name, last_name, settings, is_active, is_verified, is_system_admin, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, true, false, false, $8, $8)
	`, userID, tenantID, input.Email, passwordHash, input.FirstName, input.LastName, defaultUserSettings, now)
	if err != nil {
		h.log.Error("Failed to create user", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Create owner role for tenant owner
	roleID := uuid.New()
	_, err = tx.Exec(`
		INSERT INTO roles (id, tenant_id, name, code, description, is_system)
		VALUES ($1, $2, 'Owner', 'owner', 'Tenant owner with full access', true)
	`, roleID, tenantID)
	if err != nil {
		h.log.Error("Failed to create role", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Assign role to user
	_, err = tx.Exec(`
		INSERT INTO user_roles (user_id, role_id)
		VALUES ($1, $2)
	`, userID, roleID)
	if err != nil {
		h.log.Error("Failed to assign role", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Create default warehouse for the tenant
	warehouseID := uuid.New()
	_, err = tx.Exec(`
		INSERT INTO warehouses (id, tenant_id, code, name, is_default, is_active, reception_steps, delivery_steps)
		VALUES ($1, $2, 'WH-MAIN', 'Main Warehouse', true, true, 1, 1)
	`, warehouseID, tenantID)
	if err != nil {
		h.log.Error("Failed to create default warehouse", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Create default warehouse locations (Stock, Input, Output)
	stockLocationID := uuid.New()
	_, err = tx.Exec(`
		INSERT INTO warehouse_locations (id, warehouse_id, code, name, type, is_active)
		VALUES ($1, $2, 'STOCK', 'Stock', 'storage', true)
	`, stockLocationID, warehouseID)
	if err != nil {
		h.log.Error("Failed to create stock location", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Generate tokens - new tenant owners are NOT system admins
	tokenPair, err := h.jwtManager.GenerateTokenPair(userID, tenantID, input.Email, false)
	if err != nil {
		h.log.Error("Failed to generate tokens", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Build response - tenant owners are NOT system admins but have owner role
	ownerRoleDesc := "Tenant owner with full access"
	user := &entity.UserResponse{
		ID:            userID,
		TenantID:      tenantID,
		Email:         input.Email,
		FirstName:     input.FirstName,
		LastName:      input.LastName,
		FullName:      input.FirstName + " " + input.LastName,
		Language:      "en",
		Timezone:      "UTC",
		IsActive:      true,
		IsSystemAdmin: false,
		Roles: []entity.Role{
			{
				ID:          roleID,
				TenantID:    tenantID,
				Name:        "Owner",
				Code:        "owner",
				Description: &ownerRoleDesc,
				IsSystem:    true,
				CreatedAt:   now,
				UpdatedAt:   now,
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	tenant := &TenantInfo{
		ID:   tenantID,
		Code: input.TenantCode,
		Name: input.TenantName,
	}

	response.Created(c, AuthResponse{
		User:         user,
		Tenant:       tenant,
		AccessToken:  tokenPair.AccessToken,
		RefreshToken: tokenPair.RefreshToken,
		ExpiresAt:    tokenPair.ExpiresAt,
		TokenType:    tokenPair.TokenType,
	})
}

// Login handles user login
func (h *Handler) Login(c *gin.Context) {
	var input LoginInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Build query based on whether tenant_id is provided
	var query string
	var args []interface{}

	if input.TenantID != "" {
		tenantUUID, err := uuid.Parse(input.TenantID)
		if err != nil {
			response.BadRequest(c, "Invalid tenant ID")
			return
		}
		query = `
			SELECT id, tenant_id, email, password_hash, first_name, last_name,
			       phone, avatar_url, language, timezone, is_active, is_verified,
			       is_system_admin, failed_login_attempts, locked_until, created_at, updated_at
			FROM users
			WHERE tenant_id = $1 AND email = $2 AND deleted_at IS NULL
		`
		args = []interface{}{tenantUUID, input.Email}
	} else {
		// Check if email exists in multiple tenants
		var userCount int
		err := h.db.QueryRow("SELECT COUNT(*) FROM users WHERE email = $1 AND deleted_at IS NULL", input.Email).Scan(&userCount)
		if err != nil {
			h.log.Error("Failed to count users", "error", err)
			response.InternalServerError(c, "")
			return
		}

		if userCount > 1 {
			// Multiple accounts with same email - return list of tenants for user to choose
			rows, err := h.db.Query(`
				SELECT t.id, t.name, t.code
				FROM users u
				JOIN tenants t ON u.tenant_id = t.id
				WHERE u.email = $1 AND u.deleted_at IS NULL AND t.is_active = true
			`, input.Email)
			if err != nil {
				h.log.Error("Failed to query tenants", "error", err)
				response.InternalServerError(c, "")
				return
			}
			defer rows.Close()

			var tenants []map[string]interface{}
			for rows.Next() {
				var tenantID uuid.UUID
				var tenantName, tenantCode string
				if err := rows.Scan(&tenantID, &tenantName, &tenantCode); err != nil {
					continue
				}
				tenants = append(tenants, map[string]interface{}{
					"id":   tenantID,
					"name": tenantName,
					"code": tenantCode,
				})
			}

			response.Error(c, http.StatusConflict, "TENANT_SELECTION_REQUIRED",
				"This email is associated with multiple companies. Please select one.")
			c.JSON(http.StatusConflict, gin.H{
				"success": false,
				"error": gin.H{
					"code":    "TENANT_SELECTION_REQUIRED",
					"message": "This email is associated with multiple companies. Please select one.",
				},
				"data": gin.H{
					"tenants": tenants,
				},
			})
			return
		}

		// Single user - proceed with login
		query = `
			SELECT id, tenant_id, email, password_hash, first_name, last_name,
			       phone, avatar_url, language, timezone, is_active, is_verified,
			       is_system_admin, failed_login_attempts, locked_until, created_at, updated_at
			FROM users
			WHERE email = $1 AND deleted_at IS NULL
		`
		args = []interface{}{input.Email}
	}

	var user entity.User
	var phone, avatarURL sql.NullString
	var lockedUntil sql.NullTime

	err := h.db.QueryRow(query, args...).Scan(
		&user.ID, &user.TenantID, &user.Email, &user.PasswordHash,
		&user.FirstName, &user.LastName, &phone, &avatarURL,
		&user.Language, &user.Timezone, &user.IsActive, &user.IsVerified,
		&user.IsSystemAdmin, &user.FailedLoginAttempts, &lockedUntil,
		&user.CreatedAt, &user.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.Error(c, http.StatusUnauthorized, response.ErrCodeInvalidCredentials, "Invalid email or password")
		return
	}
	if err != nil {
		h.log.Error("Failed to query user", "error", err)
		response.InternalServerError(c, "")
		return
	}

	if phone.Valid {
		user.Phone = &phone.String
	}
	if avatarURL.Valid {
		user.AvatarURL = &avatarURL.String
	}
	if lockedUntil.Valid {
		user.LockedUntil = &lockedUntil.Time
	}

	// Check if account is locked
	if user.IsLocked() {
		response.Error(c, http.StatusUnauthorized, response.ErrCodeAccountLocked, "Account is locked. Please try again later.")
		return
	}

	// Check if account is active
	if !user.IsActive {
		response.Error(c, http.StatusUnauthorized, response.ErrCodeAccountDisabled, "Account is disabled")
		return
	}

	// Verify password
	valid, err := crypto.VerifyPassword(input.Password, user.PasswordHash)
	if err != nil || !valid {
		// Increment failed login attempts
		h.db.Exec(`
			UPDATE users SET
				failed_login_attempts = failed_login_attempts + 1,
				locked_until = CASE WHEN failed_login_attempts >= 4 THEN NOW() + INTERVAL '15 minutes' ELSE locked_until END
			WHERE id = $1
		`, user.ID)

		response.Error(c, http.StatusUnauthorized, response.ErrCodeInvalidCredentials, "Invalid email or password")
		return
	}

	// Reset failed login attempts and update last login
	now := time.Now()
	h.db.Exec(`
		UPDATE users SET
			failed_login_attempts = 0,
			locked_until = NULL,
			last_login_at = $1
		WHERE id = $2
	`, now, user.ID)

	// Generate tokens
	tokenPair, err := h.jwtManager.GenerateTokenPair(user.ID, user.TenantID, user.Email, user.IsSystemAdmin)
	if err != nil {
		h.log.Error("Failed to generate tokens", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Store refresh token hash
	tokenHash := crypto.HashToken(tokenPair.RefreshToken)
	deviceInfo, _ := json.Marshal(map[string]string{
		"user_agent": c.GetHeader("User-Agent"),
	})

	h.db.Exec(`
		INSERT INTO refresh_tokens (user_id, token_hash, device_info, ip_address, expires_at)
		VALUES ($1, $2, $3, $4, $5)
	`, user.ID, tokenHash, deviceInfo, c.ClientIP(), tokenPair.ExpiresAt.Add(h.config.JWT.RefreshTokenExpiry))

	user.LastLoginAt = &now

	// Get tenant info
	var tenantCode, tenantName string
	h.db.QueryRow("SELECT code, name FROM tenants WHERE id = $1", user.TenantID).Scan(&tenantCode, &tenantName)

	tenant := &TenantInfo{
		ID:   user.TenantID,
		Code: tenantCode,
		Name: tenantName,
	}

	response.Success(c, AuthResponse{
		User:         user.ToResponse(),
		Tenant:       tenant,
		AccessToken:  tokenPair.AccessToken,
		RefreshToken: tokenPair.RefreshToken,
		ExpiresAt:    tokenPair.ExpiresAt,
		TokenType:    tokenPair.TokenType,
	})
}

// RefreshToken handles token refresh
func (h *Handler) RefreshToken(c *gin.Context) {
	var input RefreshTokenInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Validate refresh token
	tokenPair, err := h.jwtManager.RefreshTokens(input.RefreshToken)
	if err != nil {
		if err == crypto.ErrTokenExpired {
			response.Error(c, http.StatusUnauthorized, response.ErrCodeTokenExpired, "Refresh token has expired")
		} else {
			response.Error(c, http.StatusUnauthorized, response.ErrCodeTokenInvalid, "Invalid refresh token")
		}
		return
	}

	response.Success(c, gin.H{
		"access_token":  tokenPair.AccessToken,
		"refresh_token": tokenPair.RefreshToken,
		"expires_at":    tokenPair.ExpiresAt,
		"token_type":    tokenPair.TokenType,
	})
}

// Logout handles user logout
func (h *Handler) Logout(c *gin.Context) {
	claims, ok := middleware.GetClaims(c)
	if !ok {
		response.Unauthorized(c, "")
		return
	}

	// Revoke all refresh tokens for this session
	h.db.Exec(`
		UPDATE refresh_tokens SET revoked_at = NOW()
		WHERE user_id = $1 AND revoked_at IS NULL
	`, claims.UserID)

	response.Success(c, gin.H{"message": "Logged out successfully"})
}

// GetCurrentUser returns the current authenticated user
func (h *Handler) GetCurrentUser(c *gin.Context) {
	claims, ok := middleware.GetClaims(c)
	if !ok {
		response.Unauthorized(c, "")
		return
	}

	var user entity.User
	var phone, avatarURL sql.NullString

	err := h.db.QueryRow(`
		SELECT id, tenant_id, email, first_name, last_name, phone, avatar_url,
		       language, timezone, is_active, is_verified, is_system_admin,
		       last_login_at, created_at, updated_at
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
	`, claims.UserID).Scan(
		&user.ID, &user.TenantID, &user.Email, &user.FirstName, &user.LastName,
		&phone, &avatarURL, &user.Language, &user.Timezone,
		&user.IsActive, &user.IsVerified, &user.IsSystemAdmin,
		&user.LastLoginAt, &user.CreatedAt, &user.UpdatedAt,
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
		user.Phone = &phone.String
	}
	if avatarURL.Valid {
		user.AvatarURL = &avatarURL.String
	}

	// Load roles
	rows, err := h.db.Query(`
		SELECT r.id, r.name, r.code
		FROM roles r
		JOIN user_roles ur ON r.id = ur.role_id
		WHERE ur.user_id = $1
	`, user.ID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var role entity.Role
			rows.Scan(&role.ID, &role.Name, &role.Code)
			user.Roles = append(user.Roles, role)
		}
	}

	response.Success(c, user.ToResponse())
}

// UpdateCurrentUser updates the current user's profile
func (h *Handler) UpdateCurrentUser(c *gin.Context) {
	claims, ok := middleware.GetClaims(c)
	if !ok {
		response.Unauthorized(c, "")
		return
	}

	var input entity.UpdateUserInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Build update query
	updates := []string{}
	args := []interface{}{}
	argNum := 1

	if input.FirstName != nil {
		updates = append(updates, "first_name = $"+string(rune('0'+argNum)))
		args = append(args, *input.FirstName)
		argNum++
	}
	if input.LastName != nil {
		updates = append(updates, "last_name = $"+string(rune('0'+argNum)))
		args = append(args, *input.LastName)
		argNum++
	}
	if input.Phone != nil {
		updates = append(updates, "phone = $"+string(rune('0'+argNum)))
		args = append(args, *input.Phone)
		argNum++
	}
	if input.AvatarURL != nil {
		updates = append(updates, "avatar_url = $"+string(rune('0'+argNum)))
		args = append(args, *input.AvatarURL)
		argNum++
	}
	if input.Language != nil {
		updates = append(updates, "language = $"+string(rune('0'+argNum)))
		args = append(args, *input.Language)
		argNum++
	}
	if input.Timezone != nil {
		updates = append(updates, "timezone = $"+string(rune('0'+argNum)))
		args = append(args, *input.Timezone)
		argNum++
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	args = append(args, claims.UserID)

	// Execute update
	query := "UPDATE users SET " + joinStrings(updates, ", ") + ", updated_at = NOW() WHERE id = $" + string(rune('0'+argNum))
	_, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update user", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Return updated user
	h.GetCurrentUser(c)
}

// ChangePassword changes the current user's password
func (h *Handler) ChangePassword(c *gin.Context) {
	claims, ok := middleware.GetClaims(c)
	if !ok {
		response.Unauthorized(c, "")
		return
	}

	var input entity.ChangePasswordInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Get current password hash
	var currentHash string
	err := h.db.QueryRow("SELECT password_hash FROM users WHERE id = $1", claims.UserID).Scan(&currentHash)
	if err != nil {
		h.log.Error("Failed to get user", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Verify current password
	valid, err := crypto.VerifyPassword(input.CurrentPassword, currentHash)
	if err != nil || !valid {
		response.BadRequest(c, "Current password is incorrect")
		return
	}

	// Hash new password
	newHash, err := crypto.HashPassword(input.NewPassword)
	if err != nil {
		h.log.Error("Failed to hash password", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Update password
	_, err = h.db.Exec(`
		UPDATE users SET password_hash = $1, password_changed_at = NOW(), updated_at = NOW()
		WHERE id = $2
	`, newHash, claims.UserID)
	if err != nil {
		h.log.Error("Failed to update password", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Revoke all refresh tokens
	h.db.Exec("UPDATE refresh_tokens SET revoked_at = NOW() WHERE user_id = $1", claims.UserID)

	response.Success(c, gin.H{"message": "Password changed successfully"})
}

// ForgotPassword handles forgot password request
func (h *Handler) ForgotPassword(c *gin.Context) {
	var input ForgotPasswordInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Always return success to prevent email enumeration
	// In production, send email with reset link

	response.Success(c, gin.H{
		"message": "If an account exists with this email, you will receive a password reset link",
	})
}

// ResetPassword handles password reset
func (h *Handler) ResetPassword(c *gin.Context) {
	var input ResetPasswordInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// TODO: Validate reset token and update password
	// This requires a password_reset_tokens table

	response.Success(c, gin.H{"message": "Password reset successfully"})
}

// VerifyEmail handles email verification
func (h *Handler) VerifyEmail(c *gin.Context) {
	var input VerifyEmailInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// TODO: Validate verification token and mark email as verified

	response.Success(c, gin.H{"message": "Email verified successfully"})
}

// SendInvite sends an invitation to a user to set their password
func (h *Handler) SendInvite(c *gin.Context) {
	claims, ok := middleware.GetClaims(c)
	if !ok {
		response.Unauthorized(c, "")
		return
	}

	var input SendInviteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	userID, err := uuid.Parse(input.UserID)
	if err != nil {
		response.BadRequest(c, "Invalid user ID")
		return
	}

	// Check if the target user belongs to the same tenant
	var targetTenantID uuid.UUID
	var email, firstName, lastName string
	var passwordHash sql.NullString
	err = h.db.QueryRow(`
		SELECT tenant_id, email, first_name, last_name, password_hash
		FROM users WHERE id = $1 AND deleted_at IS NULL
	`, userID).Scan(&targetTenantID, &email, &firstName, &lastName, &passwordHash)

	if err == sql.ErrNoRows {
		response.NotFound(c, "User")
		return
	}
	if err != nil {
		h.log.Error("Failed to fetch user", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Ensure same tenant
	if targetTenantID != claims.TenantID {
		response.Forbidden(c, "Cannot invite users from other tenants")
		return
	}

	// Check if user already has a password
	if passwordHash.Valid && passwordHash.String != "" {
		response.BadRequest(c, "User already has a password set")
		return
	}

	// Generate invite token
	inviteToken := uuid.New().String() + "-" + uuid.New().String()
	expiresAt := time.Now().Add(48 * time.Hour) // 48 hours validity

	// Update user with invite token
	_, err = h.db.Exec(`
		UPDATE users SET
			invite_token = $1,
			invite_token_expires = $2,
			invited_by = $3,
			invited_at = NOW(),
			updated_at = NOW()
		WHERE id = $4
	`, inviteToken, expiresAt, claims.UserID, userID)

	if err != nil {
		h.log.Error("Failed to set invite token", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Get tenant name for email
	var tenantName string
	h.db.QueryRow("SELECT name FROM tenants WHERE id = $1", claims.TenantID).Scan(&tenantName)

	// Build invite link
	inviteLink := h.config.App.FrontendURL + "/accept-invite?token=" + inviteToken

	// Send invitation email
	if err := h.emailService.SendInvite(email, firstName, lastName, tenantName, inviteLink); err != nil {
		h.log.Error("Failed to send invitation email", "error", err, "email", email)
		// Don't fail the request - the invite token is set, user can still use the link
	}

	response.Success(c, gin.H{
		"message":     "Invitation sent successfully",
		"invite_link": inviteLink, // For development/testing - copy to clipboard
		"expires_at":  expiresAt,
		"user": gin.H{
			"id":         userID,
			"email":      email,
			"first_name": firstName,
			"last_name":  lastName,
		},
	})
}

// ValidateInvite validates an invitation token without consuming it
func (h *Handler) ValidateInvite(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		response.BadRequest(c, "Token is required")
		return
	}

	var userID, tenantID uuid.UUID
	var email, firstName, lastName string
	var expiresAt time.Time

	err := h.db.QueryRow(`
		SELECT id, tenant_id, email, first_name, last_name, invite_token_expires
		FROM users
		WHERE invite_token = $1 AND deleted_at IS NULL
	`, token).Scan(&userID, &tenantID, &email, &firstName, &lastName, &expiresAt)

	if err == sql.ErrNoRows {
		response.Success(c, ValidateInviteResponse{Valid: false})
		return
	}
	if err != nil {
		h.log.Error("Failed to validate invite token", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Check if token has expired
	if time.Now().After(expiresAt) {
		response.Success(c, ValidateInviteResponse{Valid: false})
		return
	}

	// Get tenant name
	var tenantName string
	h.db.QueryRow("SELECT name FROM tenants WHERE id = $1", tenantID).Scan(&tenantName)

	response.Success(c, ValidateInviteResponse{
		Valid:      true,
		Email:      email,
		FirstName:  firstName,
		LastName:   lastName,
		TenantName: tenantName,
		ExpiresAt:  expiresAt.Format(time.RFC3339),
	})
}

// AcceptInvite accepts an invitation and sets the user's password
func (h *Handler) AcceptInvite(c *gin.Context) {
	var input AcceptInviteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Find user by invite token
	var userID, tenantID uuid.UUID
	var email, firstName, lastName string
	var expiresAt time.Time

	err := h.db.QueryRow(`
		SELECT id, tenant_id, email, first_name, last_name, invite_token_expires
		FROM users
		WHERE invite_token = $1 AND deleted_at IS NULL
	`, input.Token).Scan(&userID, &tenantID, &email, &firstName, &lastName, &expiresAt)

	if err == sql.ErrNoRows {
		response.BadRequest(c, "Invalid or expired invitation token")
		return
	}
	if err != nil {
		h.log.Error("Failed to find user by invite token", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Check if token has expired
	if time.Now().After(expiresAt) {
		response.BadRequest(c, "Invitation has expired. Please request a new invitation.")
		return
	}

	// Hash the new password
	passwordHash, err := crypto.HashPassword(input.Password)
	if err != nil {
		h.log.Error("Failed to hash password", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Update user: set password, clear invite token, mark as verified
	now := time.Now()
	_, err = h.db.Exec(`
		UPDATE users SET
			password_hash = $1,
			invite_token = NULL,
			invite_token_expires = NULL,
			is_verified = true,
			is_active = true,
			updated_at = $2
		WHERE id = $3
	`, passwordHash, now, userID)

	if err != nil {
		h.log.Error("Failed to update user password", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Check if user is admin
	var isSystemAdmin bool
	h.db.QueryRow("SELECT is_system_admin FROM users WHERE id = $1", userID).Scan(&isSystemAdmin)

	// Generate tokens for auto-login
	tokenPair, err := h.jwtManager.GenerateTokenPair(userID, tenantID, email, isSystemAdmin)
	if err != nil {
		h.log.Error("Failed to generate tokens", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Store refresh token
	tokenHash := crypto.HashToken(tokenPair.RefreshToken)
	deviceInfo, _ := json.Marshal(map[string]string{
		"user_agent": c.GetHeader("User-Agent"),
	})
	h.db.Exec(`
		INSERT INTO refresh_tokens (user_id, token_hash, device_info, ip_address, expires_at)
		VALUES ($1, $2, $3, $4, $5)
	`, userID, tokenHash, deviceInfo, c.ClientIP(), tokenPair.ExpiresAt.Add(h.config.JWT.RefreshTokenExpiry))

	// Get tenant info
	var tenantCode, tenantName string
	h.db.QueryRow("SELECT code, name FROM tenants WHERE id = $1", tenantID).Scan(&tenantCode, &tenantName)

	// Build user response
	user := &entity.UserResponse{
		ID:            userID,
		TenantID:      tenantID,
		Email:         email,
		FirstName:     firstName,
		LastName:      lastName,
		FullName:      firstName + " " + lastName,
		Language:      "en",
		Timezone:      "UTC",
		IsActive:      true,
		IsVerified:    true,
		IsSystemAdmin: isSystemAdmin,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	tenant := &TenantInfo{
		ID:   tenantID,
		Code: tenantCode,
		Name: tenantName,
	}

	response.Success(c, AuthResponse{
		User:         user,
		Tenant:       tenant,
		AccessToken:  tokenPair.AccessToken,
		RefreshToken: tokenPair.RefreshToken,
		ExpiresAt:    tokenPair.ExpiresAt,
		TokenType:    tokenPair.TokenType,
	})
}

// Helper function
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for _, s := range strs[1:] {
		result += sep + s
	}
	return result
}
