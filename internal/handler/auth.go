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

// AuthResponse represents authentication response
type AuthResponse struct {
	User        *entity.UserResponse `json:"user"`
	AccessToken string               `json:"access_token"`
	RefreshToken string              `json:"refresh_token"`
	ExpiresAt   time.Time            `json:"expires_at"`
	TokenType   string               `json:"token_type"`
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

	// Check if email exists within tenant
	var existingUserID uuid.UUID
	err = tx.QueryRow("SELECT id FROM users WHERE tenant_id = $1 AND email = $2", tenantID, input.Email).Scan(&existingUserID)
	if err == nil {
		response.Conflict(c, "Email already exists")
		return
	}
	if err != sql.ErrNoRows {
		h.log.Error("Failed to check user", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Create user
	userID := uuid.New()
	now := time.Now()
	defaultUserSettings, _ := json.Marshal(map[string]interface{}{})

	_, err = tx.Exec(`
		INSERT INTO users (id, tenant_id, email, password_hash, first_name, last_name, settings, is_active, is_verified, is_system_admin, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, true, false, true, $8, $8)
	`, userID, tenantID, input.Email, passwordHash, input.FirstName, input.LastName, defaultUserSettings, now)
	if err != nil {
		h.log.Error("Failed to create user", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Create default admin role
	roleID := uuid.New()
	_, err = tx.Exec(`
		INSERT INTO roles (id, tenant_id, name, code, description, is_system)
		VALUES ($1, $2, 'Administrator', 'admin', 'Full system access', true)
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

	// Commit transaction
	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Generate tokens
	tokenPair, err := h.jwtManager.GenerateTokenPair(userID, tenantID, input.Email, true)
	if err != nil {
		h.log.Error("Failed to generate tokens", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Build response
	user := &entity.UserResponse{
		ID:        userID,
		Email:     input.Email,
		FirstName: input.FirstName,
		LastName:  input.LastName,
		FullName:  input.FirstName + " " + input.LastName,
		Language:  "en",
		Timezone:  "UTC",
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	}

	response.Created(c, AuthResponse{
		User:         user,
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
		// Find first matching user by email
		query = `
			SELECT id, tenant_id, email, password_hash, first_name, last_name,
			       phone, avatar_url, language, timezone, is_active, is_verified,
			       is_system_admin, failed_login_attempts, locked_until, created_at, updated_at
			FROM users
			WHERE email = $1 AND deleted_at IS NULL
			LIMIT 1
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

	response.Success(c, AuthResponse{
		User:         user.ToResponse(),
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
