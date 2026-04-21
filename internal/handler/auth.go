package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/crypto"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"google.golang.org/api/idtoken"
)

// normalizePhone strips non-digit chars and ensures 998 prefix for Uzbekistan
func normalizePhone(phone string) string {
	var buf strings.Builder
	for _, c := range phone {
		if c >= '0' && c <= '9' {
			buf.WriteRune(c)
		}
	}
	result := buf.String()
	if !strings.HasPrefix(result, "998") && len(result) == 9 {
		result = "998" + result
	}
	return result
}

// LoginInput represents login request
type LoginInput struct {
	Email    string `json:"email" binding:"-"`
	Phone    string `json:"phone" binding:"-"`
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

// SendOTPInput represents OTP request (either email or phone must be provided)
type SendOTPInput struct {
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	Purpose  string `json:"purpose" binding:"required"` // registration, password_reset
	Language string `json:"language"`                   // en, uz, ru - defaults to uz
}

// VerifyOTPInput represents OTP verification request (either email or phone)
type VerifyOTPInput struct {
	Email   string `json:"email"`
	Phone   string `json:"phone"`
	OTPCode string `json:"otp_code" binding:"required,len=6"`
	Purpose string `json:"purpose" binding:"required"`
}

// SendPhoneOTPInput represents phone-based OTP request
type SendPhoneOTPInput struct {
	Phone    string `json:"phone" binding:"required"`
	Purpose  string `json:"purpose" binding:"required"` // password_reset
	Language string `json:"language"`                   // en, uz, ru
}

// VerifyPhoneOTPInput represents phone-based OTP verification request
type VerifyPhoneOTPInput struct {
	Phone   string `json:"phone" binding:"required"`
	OTPCode string `json:"otp_code" binding:"required,len=6"`
	Purpose string `json:"purpose" binding:"required"`
}

// ResetPasswordWithPhoneInput represents password reset with phone verification
type ResetPasswordWithPhoneInput struct {
	Phone       string `json:"phone" binding:"required"`
	OTPCode     string `json:"otp_code" binding:"required,len=6"`
	NewPassword string `json:"new_password" binding:"required,min=8"`
}

// RegisterWithOTPInput represents registration with OTP verification
type RegisterWithOTPInput struct {
	TenantCode string `json:"tenant_code" binding:"required,min=2,max=50"`
	TenantName string `json:"tenant_name" binding:"required,min=2,max=255"`
	Email      string `json:"email"`
	Phone      string `json:"phone"`
	Password   string `json:"password" binding:"required,min=8"`
	FirstName  string `json:"first_name" binding:"required,min=1,max=100"`
	LastName   string `json:"last_name" binding:"required,min=1,max=100"`
	OTPCode    string `json:"otp_code" binding:"required,len=6"`
}

// GoogleAuthInput represents Google authentication request
type GoogleAuthInput struct {
	Credential  string `json:"credential" binding:"required"`
	TenantID    string `json:"tenant_id,omitempty"`
	CompanyName string `json:"company_name,omitempty"`
}

// GoogleAuthNeedsCompletionResponse returned when a new Google user needs to provide company name
type GoogleAuthNeedsCompletionResponse struct {
	NeedsCompletion bool              `json:"needs_completion"`
	GoogleUser      GoogleUserPreview `json:"google_user"`
}

// GoogleUserPreview holds Google profile info for the completion step
type GoogleUserPreview struct {
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Picture   string `json:"picture,omitempty"`
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
// Register godoc
// @Summary Register new user and tenant
// @Description Create a new tenant and admin user account
// @Tags Authentication
// @Accept json
// @Produce json
// @Param register body RegisterInput true "Registration details"
// @Success 201 {object} response.Response{data=AuthResponse} "Successfully registered"
// @Failure 400 {object} response.Response "Invalid input or user already exists"
// @Failure 500 {object} response.Response "Internal server error"
// @Router /auth/register [post]
func (h *Handler) Register(c *gin.Context) {
	var input RegisterInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
		INSERT INTO users (id, tenant_id, email, password_hash, first_name, last_name, settings, is_active, is_verified, is_system_admin, role, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, true, false, false, 'owner', $8, $8)
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

	// Seed default units of measure
	h.seedDefaultUnitsOfMeasure(tx, tenantID)

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
// Login godoc
// @Summary User login
// @Description Authenticate user with email and password
// @Tags Authentication
// @Accept json
// @Produce json
// @Param login body LoginInput true "Login credentials"
// @Success 200 {object} response.Response{data=AuthResponse} "Successfully authenticated"
// @Failure 400 {object} response.Response "Invalid input"
// @Failure 401 {object} response.Response "Invalid credentials"
// @Failure 500 {object} response.Response "Internal server error"
// @Router /auth/login [post]
func (h *Handler) Login(c *gin.Context) {
	var input LoginInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Validate: at least email or phone must be provided
	if input.Email == "" && input.Phone == "" {
		response.BadRequest(c, "Email or phone number is required")
		return
	}

	// Determine login method: phone or email
	loginByPhone := input.Phone != "" && input.Email == ""

	// Normalize phone number if logging in by phone
	if loginByPhone {
		input.Phone = normalizePhone(input.Phone)
	}

	// The column and value to search by
	lookupColumn := "email"
	lookupValue := input.Email
	if loginByPhone {
		lookupColumn = "phone"
		lookupValue = input.Phone
	}

	selectFields := `id, tenant_id, COALESCE(email, ''), password_hash, first_name, last_name,
		phone, avatar_url, language, timezone, is_active, is_verified,
		is_system_admin, failed_login_attempts, locked_until, created_at, updated_at`

	// Build query based on whether tenant_id is provided
	var query string
	var args []interface{}

	if input.TenantID != "" {
		tenantUUID, err := uuid.Parse(input.TenantID)
		if err != nil {
			response.BadRequest(c, "Invalid tenant ID")
			return
		}
		query = fmt.Sprintf(`SELECT %s FROM users WHERE tenant_id = $1 AND %s = $2 AND deleted_at IS NULL`, selectFields, lookupColumn)
		args = []interface{}{tenantUUID, lookupValue}
	} else {
		// Check if identifier exists in multiple tenants
		var userCount int
		err := h.db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM users WHERE %s = $1 AND deleted_at IS NULL", lookupColumn), lookupValue).Scan(&userCount)
		if err != nil {
			h.log.Error("Failed to count users", "error", err)
			response.InternalServerError(c, "")
			return
		}

		if userCount > 1 {
			// Multiple accounts - return list of tenants for user to choose
			rows, err := h.db.Query(fmt.Sprintf(`
				SELECT t.id, t.name, t.code
				FROM users u
				JOIN tenants t ON u.tenant_id = t.id
				WHERE u.%s = $1 AND u.deleted_at IS NULL AND t.is_active = true
			`, lookupColumn), lookupValue)
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
				"This account is associated with multiple companies. Please select one.")
			c.JSON(http.StatusConflict, gin.H{
				"success": false,
				"error": gin.H{
					"code":    "TENANT_SELECTION_REQUIRED",
					"message": "This account is associated with multiple companies. Please select one.",
				},
				"data": gin.H{
					"tenants": tenants,
				},
			})
			return
		}

		// Single user - proceed with login
		query = fmt.Sprintf(`SELECT %s FROM users WHERE %s = $1 AND deleted_at IS NULL`, selectFields, lookupColumn)
		args = []interface{}{lookupValue}
	}

	var user entity.User
	var phone, avatarURL, passwordHash sql.NullString
	var lockedUntil sql.NullTime

	err := h.db.QueryRow(query, args...).Scan(
		&user.ID, &user.TenantID, &user.Email, &passwordHash,
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

	if passwordHash.Valid {
		user.PasswordHash = passwordHash.String
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

	// Check if this is a Google-only account (no password)
	if !passwordHash.Valid || passwordHash.String == "" {
		response.Error(c, http.StatusUnauthorized, response.ErrCodeInvalidCredentials, "This account uses Google Sign-In. Please sign in with Google.")
		return
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

	// Load user roles
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
// RefreshToken godoc
// @Summary Refresh access token
// @Description Get new access and refresh tokens using a valid refresh token
// @Tags Authentication
// @Accept json
// @Produce json
// @Param refresh body RefreshTokenInput true "Refresh token"
// @Success 200 {object} response.Response{data=AuthResponse} "Successfully refreshed tokens"
// @Failure 400 {object} response.Response "Invalid input"
// @Failure 401 {object} response.Response "Invalid or expired refresh token"
// @Failure 500 {object} response.Response "Internal server error"
// @Router /auth/refresh [post]
func (h *Handler) RefreshToken(c *gin.Context) {
	var input RefreshTokenInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
		SELECT id, tenant_id, COALESCE(email, ''), first_name, last_name, phone, avatar_url,
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build update query
	updates := []string{}
	args := []interface{}{}
	argNum := 1

	if input.FirstName != nil {
		updates = append(updates, fmt.Sprintf("first_name = $%d", argNum))
		args = append(args, *input.FirstName)
		argNum++
	}
	if input.LastName != nil {
		updates = append(updates, fmt.Sprintf("last_name = $%d", argNum))
		args = append(args, *input.LastName)
		argNum++
	}
	if input.Phone != nil {
		updates = append(updates, fmt.Sprintf("phone = $%d", argNum))
		args = append(args, *input.Phone)
		argNum++
	}
	if input.Email != nil {
		updates = append(updates, fmt.Sprintf("email = $%d", argNum))
		args = append(args, *input.Email)
		argNum++
	}
	if input.AvatarURL != nil {
		updates = append(updates, fmt.Sprintf("avatar_url = $%d", argNum))
		args = append(args, *input.AvatarURL)
		argNum++
	}
	if input.Language != nil {
		updates = append(updates, fmt.Sprintf("language = $%d", argNum))
		args = append(args, *input.Language)
		argNum++
	}
	if input.Timezone != nil {
		updates = append(updates, fmt.Sprintf("timezone = $%d", argNum))
		args = append(args, *input.Timezone)
		argNum++
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	args = append(args, claims.UserID)

	// Execute update
	query := "UPDATE users SET " + joinStrings(updates, ", ") + fmt.Sprintf(", updated_at = NOW() WHERE id = $%d", argNum)
	_, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update user", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Sync phone/email changes to linked employee record
	if input.Phone != nil {
		h.db.Exec("UPDATE employees SET phone = $1, updated_at = NOW() WHERE user_id = $2 AND deleted_at IS NULL", *input.Phone, claims.UserID)
	}
	if input.Email != nil {
		h.db.Exec("UPDATE employees SET email = $1, updated_at = NOW() WHERE user_id = $2 AND deleted_at IS NULL", *input.Email, claims.UserID)
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Always return success to prevent email enumeration
	successMsg := "If an account exists with this email, you will receive a password reset link"

	// Look up user by email
	var userID uuid.UUID
	var firstName string
	err := h.db.QueryRow(`
		SELECT id, first_name FROM users
		WHERE email = $1 AND deleted_at IS NULL
		LIMIT 1
	`, strings.ToLower(strings.TrimSpace(input.Email))).Scan(&userID, &firstName)

	if err != nil {
		// User not found — return success anyway to prevent enumeration
		response.Success(c, gin.H{"message": successMsg})
		return
	}

	// Generate reset token
	resetToken := uuid.New().String() + "-" + uuid.New().String()
	expiresAt := time.Now().Add(1 * time.Hour)

	// Store token in DB
	_, err = h.db.Exec(`
		UPDATE users SET
			password_reset_token = $1,
			password_reset_token_expires = $2,
			updated_at = NOW()
		WHERE id = $3
	`, resetToken, expiresAt, userID)

	if err != nil {
		h.log.Error("Failed to store password reset token", "error", err)
		response.Success(c, gin.H{"message": successMsg})
		return
	}

	// Build reset link and send email
	resetLink := h.config.App.FrontendURL + "/reset-password?token=" + resetToken

	if err := h.emailService.SendPasswordReset(input.Email, firstName, resetLink); err != nil {
		h.log.Error("Failed to send password reset email", "error", err, "email", input.Email)
		// Don't fail — token is stored, could be resent
	}

	response.Success(c, gin.H{"message": successMsg})
}

// ResetPassword handles password reset
func (h *Handler) ResetPassword(c *gin.Context) {
	var input ResetPasswordInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Look up user by valid, non-expired token
	var userID uuid.UUID
	err := h.db.QueryRow(`
		SELECT id FROM users
		WHERE password_reset_token = $1
		  AND password_reset_token_expires > NOW()
		  AND deleted_at IS NULL
	`, input.Token).Scan(&userID)

	if err != nil {
		response.BadRequest(c, "Invalid or expired reset token")
		return
	}

	// Hash the new password
	hashedPassword, err := crypto.HashPassword(input.NewPassword)
	if err != nil {
		h.log.Error("Failed to hash password", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Update password and clear reset token
	_, err = h.db.Exec(`
		UPDATE users SET
			password_hash = $1,
			password_reset_token = NULL,
			password_reset_token_expires = NULL,
			password_changed_at = NOW(),
			updated_at = NOW()
		WHERE id = $2
	`, hashedPassword, userID)

	if err != nil {
		h.log.Error("Failed to reset password", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Revoke all refresh tokens for security
	h.db.Exec("UPDATE refresh_tokens SET revoked_at = NOW() WHERE user_id = $1", userID)

	response.Success(c, gin.H{"message": "Password reset successfully"})
}

// SendPasswordResetOTP sends an OTP code via SMS to the specified phone number for password reset
func (h *Handler) SendPasswordResetOTP(c *gin.Context) {
	var input SendPhoneOTPInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	if input.Purpose != "password_reset" {
		response.BadRequest(c, "Invalid purpose. Must be 'password_reset'")
		return
	}

	phone := normalizePhone(input.Phone)

	fmt.Printf("[OTP-DEBUG] Input phone: %s, Normalized: %s\n", input.Phone, phone)

	// Check if an employee exists with this phone number (user account may or may not exist)
	var employeeID uuid.UUID
	err := h.db.QueryRow(`
		SELECT id FROM employees
		WHERE REGEXP_REPLACE(phone, '[^0-9]', '', 'g') = $1 AND deleted_at IS NULL
		LIMIT 1
	`, phone).Scan(&employeeID)

	if err == sql.ErrNoRows {
		fmt.Printf("[OTP-DEBUG] No employee found for phone: %s\n", phone)
		// Don't reveal if phone exists — return success anyway
		response.Success(c, gin.H{
			"message": "If an account exists with this phone, you will receive an OTP code",
		})
		return
	}
	if err != nil {
		h.log.Error("Failed to check phone", "error", err)
		response.InternalServerError(c, "")
		return
	}
	fmt.Printf("[OTP-DEBUG] Found employee: %s for phone: %s\n", employeeID, phone)

	// Invalidate any existing OTPs for this phone and purpose
	h.db.Exec(`
		UPDATE phone_verification_otps
		SET verified_at = NOW()
		WHERE phone = $1 AND purpose = $2 AND verified_at IS NULL
	`, phone, input.Purpose)

	// Generate 6-digit OTP code
	otpCode := generateOTPCode()
	expiresAt := time.Now().Add(10 * time.Minute)

	// Store OTP in database
	_, err = h.db.Exec(`
		INSERT INTO phone_verification_otps (phone, otp_code, purpose, expires_at)
		VALUES ($1, $2, $3, $4)
	`, phone, otpCode, input.Purpose, expiresAt)
	if err != nil {
		h.log.Error("Failed to store phone OTP", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// SMS message — use same template style as credential sending
	smsMessage := fmt.Sprintf("Sizning Genix Admin paneliga kirish kodingiz: %s", otpCode)

	// Always log OTP to console for debugging
	fmt.Printf("[OTP] Phone: %s, Code: %s\n", phone, otpCode)

	// Send OTP via SMS
	if err := h.smsService.Send(phone, smsMessage); err != nil {
		h.log.Error("Failed to send OTP SMS", "error", err, "phone", phone)
		// Don't fail — OTP is stored, user can request resend
	}

	result := gin.H{
		"message":    "OTP code sent successfully",
		"expires_at": expiresAt,
	}

	// In development mode, include OTP in response
	if h.config.App.Env == "development" || h.config.App.Env == "local" || h.config.App.Env == "" {
		result["dev_otp_code"] = otpCode
	}

	response.Success(c, result)
}

// VerifyPasswordResetOTP verifies an OTP code for the specified phone number (password reset flow)
func (h *Handler) VerifyPasswordResetOTP(c *gin.Context) {
	var input VerifyPhoneOTPInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	phone := normalizePhone(input.Phone)

	var otpID uuid.UUID
	var storedCode string
	var attempts, maxAttempts int
	var expiresAt time.Time
	var verifiedAt sql.NullTime

	err := h.db.QueryRow(`
		SELECT id, otp_code, attempts, max_attempts, expires_at, verified_at
		FROM phone_verification_otps
		WHERE phone = $1 AND purpose = $2
		ORDER BY created_at DESC
		LIMIT 1
	`, phone, input.Purpose).Scan(&otpID, &storedCode, &attempts, &maxAttempts, &expiresAt, &verifiedAt)

	if err == sql.ErrNoRows {
		response.BadRequest(c, "No OTP found. Please request a new one.")
		return
	}
	if err != nil {
		h.log.Error("Failed to query phone OTP", "error", err)
		response.InternalServerError(c, "")
		return
	}

	if verifiedAt.Valid {
		response.BadRequest(c, "OTP already used. Please request a new one.")
		return
	}

	if time.Now().After(expiresAt) {
		response.BadRequest(c, "OTP has expired. Please request a new one.")
		return
	}

	if attempts >= maxAttempts {
		response.BadRequest(c, "Too many failed attempts. Please request a new OTP.")
		return
	}

	if storedCode != input.OTPCode {
		h.db.Exec("UPDATE phone_verification_otps SET attempts = attempts + 1 WHERE id = $1", otpID)
		response.BadRequest(c, "Invalid OTP code")
		return
	}

	// Mark as verified
	_, err = h.db.Exec("UPDATE phone_verification_otps SET verified_at = NOW() WHERE id = $1", otpID)
	if err != nil {
		h.log.Error("Failed to mark phone OTP as verified", "error", err)
		response.InternalServerError(c, "")
		return
	}

	response.Success(c, gin.H{
		"message":  "Phone verified successfully",
		"verified": true,
	})
}

// ResetPasswordWithPhone handles password set/reset using phone + OTP verification
func (h *Handler) ResetPasswordWithPhone(c *gin.Context) {
	var input ResetPasswordWithPhoneInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	phone := normalizePhone(input.Phone)

	// Verify that this phone has a recently verified OTP
	var otpID uuid.UUID
	var storedCode string
	var verifiedAt sql.NullTime

	err := h.db.QueryRow(`
		SELECT id, otp_code, verified_at
		FROM phone_verification_otps
		WHERE phone = $1 AND purpose = 'password_reset'
		  AND verified_at IS NOT NULL
		  AND verified_at > NOW() - INTERVAL '15 minutes'
		ORDER BY verified_at DESC
		LIMIT 1
	`, phone).Scan(&otpID, &storedCode, &verifiedAt)

	if err == sql.ErrNoRows {
		response.BadRequest(c, "Phone not verified. Please verify your phone first.")
		return
	}
	if err != nil {
		h.log.Error("Failed to check phone verification", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Double-check OTP code matches
	if storedCode != input.OTPCode {
		response.BadRequest(c, "Invalid OTP code")
		return
	}

	// Find employee by phone
	var employeeID uuid.UUID
	var empEmail sql.NullString
	var empFirstName, empLastName string
	var empTenantID uuid.UUID
	err = h.db.QueryRow(`
		SELECT id, tenant_id, COALESCE(email, ''), first_name, last_name
		FROM employees
		WHERE REGEXP_REPLACE(phone, '[^0-9]', '', 'g') = $1 AND deleted_at IS NULL
		LIMIT 1
	`, phone).Scan(&employeeID, &empTenantID, &empEmail, &empFirstName, &empLastName)

	if err != nil {
		h.log.Error("Failed to find employee by phone", "error", err)
		response.BadRequest(c, "No account found with this phone number")
		return
	}

	// Hash the new password
	hashedPassword, err := crypto.HashPassword(input.NewPassword)
	if err != nil {
		h.log.Error("Failed to hash password", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Try to find existing user linked to this employee
	var userID uuid.UUID
	err = h.db.QueryRow(`
		SELECT u.id FROM users u
		WHERE (u.employee_id = $1 OR (u.email = $2 AND $2 != ''))
		  AND u.deleted_at IS NULL
		LIMIT 1
	`, employeeID, empEmail.String).Scan(&userID)

	if err == sql.ErrNoRows {
		// No user account exists — create one
		userID = uuid.New()
		var emailVal *string
		if empEmail.Valid && empEmail.String != "" {
			emailVal = &empEmail.String
		}
		_, err = h.db.Exec(`
			INSERT INTO users (id, tenant_id, email, password_hash, first_name, last_name, phone,
			                   language, timezone, settings, is_active, is_verified, employee_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'uz', 'Asia/Tashkent', '{}', true, true, $8)
		`, userID, empTenantID, emailVal, hashedPassword, empFirstName, empLastName, phone, employeeID)
		if err != nil {
			h.log.Error("Failed to create user for employee", "error", err)
			response.InternalServerError(c, "")
			return
		}
		fmt.Printf("[OTP] Created new user %s for employee %s\n", userID, employeeID)
	} else if err != nil {
		h.log.Error("Failed to find user", "error", err)
		response.InternalServerError(c, "")
		return
	} else {
		// User exists — update password
		_, err = h.db.Exec(`
			UPDATE users SET
				password_hash = $1,
				password_changed_at = NOW(),
				updated_at = NOW()
			WHERE id = $2
		`, hashedPassword, userID)
		if err != nil {
			h.log.Error("Failed to set password", "error", err)
			response.InternalServerError(c, "")
			return
		}
	}

	// Revoke all refresh tokens for security
	h.db.Exec("UPDATE refresh_tokens SET revoked_at = NOW() WHERE user_id = $1", userID)

	// Invalidate the used OTP
	h.db.Exec("UPDATE phone_verification_otps SET verified_at = NOW() WHERE phone = $1 AND purpose = 'password_reset' AND verified_at IS NULL", phone)

	response.Success(c, gin.H{"message": "Password set successfully"})
}

// VerifyEmail handles email verification
func (h *Handler) VerifyEmail(c *gin.Context) {
	var input VerifyEmailInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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

// SendOTP sends an OTP code to the specified email or phone for verification
func (h *Handler) SendOTP(c *gin.Context) {
	var input SendOTPInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Either email OR phone must be provided
	if input.Email == "" && input.Phone == "" {
		response.BadRequest(c, "Email or phone is required")
		return
	}

	// Validate purpose
	if input.Purpose != "registration" && input.Purpose != "password_reset" {
		response.BadRequest(c, "Invalid purpose. Must be 'registration' or 'password_reset'")
		return
	}

	// Decide identifier (phone takes precedence when provided) — used as the key in email_verification_otps.email column
	usePhone := input.Phone != ""
	if usePhone {
		input.Phone = normalizePhone(input.Phone)
	}
	identifier := input.Email
	if usePhone {
		identifier = input.Phone
	}

	// For registration, check if identifier already exists
	if input.Purpose == "registration" {
		var existingUserID uuid.UUID
		var query string
		var arg interface{}
		if usePhone {
			query = "SELECT id FROM users WHERE phone = $1 AND deleted_at IS NULL"
			arg = input.Phone
		} else {
			query = "SELECT id FROM users WHERE email = $1 AND deleted_at IS NULL"
			arg = input.Email
		}
		err := h.db.QueryRow(query, arg).Scan(&existingUserID)
		if err == nil {
			if usePhone {
				response.Conflict(c, "Phone number already registered. Please login to your existing account.")
			} else {
				response.Conflict(c, "Email already registered. Please use a different email or login to your existing account.")
			}
			return
		}
		if err != sql.ErrNoRows {
			h.log.Error("Failed to check identifier", "error", err)
			response.InternalServerError(c, "")
			return
		}
	}

	// For password_reset, check if identifier exists
	if input.Purpose == "password_reset" {
		var existingUserID uuid.UUID
		var query string
		var arg interface{}
		if usePhone {
			query = "SELECT id FROM users WHERE phone = $1 AND deleted_at IS NULL"
			arg = input.Phone
		} else {
			query = "SELECT id FROM users WHERE email = $1 AND deleted_at IS NULL"
			arg = input.Email
		}
		err := h.db.QueryRow(query, arg).Scan(&existingUserID)
		if err == sql.ErrNoRows {
			// Don't reveal if identifier exists or not for security
			response.Success(c, gin.H{
				"message": "If an account exists, you will receive an OTP code",
			})
			return
		}
		if err != nil {
			h.log.Error("Failed to check identifier", "error", err)
			response.InternalServerError(c, "")
			return
		}
	}

	// Invalidate any existing OTPs for this identifier and purpose
	_, err := h.db.Exec(`
		UPDATE email_verification_otps
		SET verified_at = NOW()
		WHERE email = $1 AND purpose = $2 AND verified_at IS NULL
	`, identifier, input.Purpose)
	if err != nil {
		h.log.Error("Failed to invalidate existing OTPs", "error", err)
		// Continue anyway
	}

	// Generate 6-digit OTP code
	otpCode := generateOTPCode()
	expiresAt := time.Now().Add(10 * time.Minute) // 10 minutes validity

	// Store OTP in database (reuse email column as a generic identifier)
	_, err = h.db.Exec(`
		INSERT INTO email_verification_otps (email, otp_code, purpose, expires_at)
		VALUES ($1, $2, $3, $4)
	`, identifier, otpCode, input.Purpose, expiresAt)
	if err != nil {
		h.log.Error("Failed to store OTP", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Send OTP via the appropriate channel
	if usePhone {
		smsMessage := fmt.Sprintf("Genix ERP tasdiqlash kodi: %s. Kodni hech kimga bermang.", otpCode)
		if err := h.smsService.Send(input.Phone, smsMessage); err != nil {
			h.log.Error("Failed to send SMS OTP", "error", err, "phone", input.Phone)
			// Don't fail the request - OTP is stored, dev mode prints code
		}
	} else {
		if err := h.emailService.SendOTP(input.Email, otpCode, input.Purpose, input.Language); err != nil {
			h.log.Error("Failed to send OTP email", "error", err, "email", input.Email)
			// Don't fail the request - OTP is stored, user can request resend
		}
	}

	result := gin.H{
		"message":    "OTP code sent successfully",
		"expires_at": expiresAt,
	}

	// In development mode, include OTP in response (email won't actually be sent)
	if h.config.App.Env == "development" {
		result["dev_otp_code"] = otpCode
	}

	response.Success(c, result)
}

// VerifyOTP verifies an OTP code for the specified email
func (h *Handler) VerifyOTP(c *gin.Context) {
	var input VerifyOTPInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	if input.Email == "" && input.Phone == "" {
		response.BadRequest(c, "Email or phone is required")
		return
	}
	identifier := input.Email
	if input.Phone != "" {
		identifier = normalizePhone(input.Phone)
	}

	// Find the OTP record
	var otpID uuid.UUID
	var storedCode string
	var attempts, maxAttempts int
	var expiresAt time.Time
	var verifiedAt sql.NullTime

	err := h.db.QueryRow(`
		SELECT id, otp_code, attempts, max_attempts, expires_at, verified_at
		FROM email_verification_otps
		WHERE email = $1 AND purpose = $2
		ORDER BY created_at DESC
		LIMIT 1
	`, identifier, input.Purpose).Scan(&otpID, &storedCode, &attempts, &maxAttempts, &expiresAt, &verifiedAt)

	if err == sql.ErrNoRows {
		response.BadRequest(c, "No OTP found. Please request a new one.")
		return
	}
	if err != nil {
		h.log.Error("Failed to query OTP", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Check if already verified
	if verifiedAt.Valid {
		response.BadRequest(c, "OTP already used. Please request a new one.")
		return
	}

	// Check if expired
	if time.Now().After(expiresAt) {
		response.BadRequest(c, "OTP has expired. Please request a new one.")
		return
	}

	// Check attempts
	if attempts >= maxAttempts {
		response.BadRequest(c, "Too many failed attempts. Please request a new OTP.")
		return
	}

	// Verify OTP code
	if storedCode != input.OTPCode {
		// Increment attempts
		h.db.Exec("UPDATE email_verification_otps SET attempts = attempts + 1 WHERE id = $1", otpID)
		response.BadRequest(c, "Invalid OTP code")
		return
	}

	// Mark as verified
	_, err = h.db.Exec("UPDATE email_verification_otps SET verified_at = NOW() WHERE id = $1", otpID)
	if err != nil {
		h.log.Error("Failed to mark OTP as verified", "error", err)
		response.InternalServerError(c, "")
		return
	}

	response.Success(c, gin.H{
		"message":  "Email verified successfully",
		"verified": true,
	})
}

// RegisterWithOTP handles user registration with OTP verification
func (h *Handler) RegisterWithOTP(c *gin.Context) {
	var input RegisterWithOTPInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	if input.Email == "" && input.Phone == "" {
		response.BadRequest(c, "Email or phone is required")
		return
	}
	if input.Phone != "" {
		input.Phone = normalizePhone(input.Phone)
	}
	identifier := input.Email
	if input.Phone != "" && input.Email == "" {
		identifier = input.Phone
	}

	// Verify OTP first (stored against the identifier actually used for sending)
	var otpID uuid.UUID
	var storedCode string
	var expiresAt time.Time
	var verifiedAt sql.NullTime

	err := h.db.QueryRow(`
		SELECT id, otp_code, expires_at, verified_at
		FROM email_verification_otps
		WHERE email = $1 AND purpose = 'registration'
		ORDER BY created_at DESC
		LIMIT 1
	`, identifier).Scan(&otpID, &storedCode, &expiresAt, &verifiedAt)

	if err == sql.ErrNoRows {
		response.BadRequest(c, "No OTP found. Please verify your email first.")
		return
	}
	if err != nil {
		h.log.Error("Failed to query OTP", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Check if expired
	if time.Now().After(expiresAt) {
		response.BadRequest(c, "OTP has expired. Please request a new one.")
		return
	}

	// Verify OTP code matches
	if storedCode != input.OTPCode {
		response.BadRequest(c, "Invalid OTP code")
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

	// Check if email/phone already exists globally (across all tenants)
	var existingUserID uuid.UUID
	if input.Email != "" {
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
	}
	if input.Phone != "" {
		err = tx.QueryRow("SELECT id FROM users WHERE phone = $1 AND deleted_at IS NULL", input.Phone).Scan(&existingUserID)
		if err == nil {
			response.Conflict(c, "Phone number already registered. Please login to your existing account.")
			return
		}
		if err != sql.ErrNoRows {
			h.log.Error("Failed to check phone", "error", err)
			response.InternalServerError(c, "")
			return
		}
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

	// Create user - email/phone is verified via OTP
	userID := uuid.New()
	now := time.Now()
	defaultUserSettings, _ := json.Marshal(map[string]interface{}{})

	// Normalize empty strings to NULL to avoid unique-constraint collisions on ""
	var emailArg interface{}
	if input.Email != "" {
		emailArg = input.Email
	}
	var phoneArg interface{}
	if input.Phone != "" {
		phoneArg = input.Phone
	}

	_, err = tx.Exec(`
		INSERT INTO users (id, tenant_id, email, phone, password_hash, first_name, last_name, settings, is_active, is_verified, is_system_admin, role, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, true, true, false, 'owner', $9, $9)
	`, userID, tenantID, emailArg, phoneArg, passwordHash, input.FirstName, input.LastName, defaultUserSettings, now)
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

	// Create default warehouse locations
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

	// Mark OTP as used
	_, err = tx.Exec("UPDATE email_verification_otps SET verified_at = NOW() WHERE id = $1", otpID)
	if err != nil {
		h.log.Error("Failed to mark OTP as used", "error", err)
		// Continue anyway - registration is more important
	}

	// Seed default units of measure
	h.seedDefaultUnitsOfMeasure(tx, tenantID)

	// Commit transaction
	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Generate tokens
	tokenPair, err := h.jwtManager.GenerateTokenPair(userID, tenantID, input.Email, false)
	if err != nil {
		h.log.Error("Failed to generate tokens", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Build response
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
		IsVerified:    true,
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

// generateOTPCode generates a random 6-digit OTP code
func generateOTPCode() string {
	// Use crypto/rand for secure random generation
	const digits = "0123456789"
	code := make([]byte, 6)
	for i := range code {
		// Generate a random number between 0-9
		num := uuid.New()[0] % 10
		code[i] = digits[num]
	}
	return string(code)
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

// GoogleAuth handles Google Sign-In and Sign-Up
func (h *Handler) GoogleAuth(c *gin.Context) {
	var input GoogleAuthInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Verify Google ID token
	clientID := h.config.Google.ClientID
	if clientID == "" {
		response.Error(c, http.StatusServiceUnavailable, response.ErrCodeServiceUnavailable, "Google Sign-In is not configured")
		return
	}

	payload, err := idtoken.Validate(context.Background(), input.Credential, clientID)
	if err != nil {
		h.log.Error("Failed to verify Google ID token", "error", err)
		response.Error(c, http.StatusUnauthorized, response.ErrCodeInvalidCredentials, "Invalid Google credential")
		return
	}

	// Extract claims from verified token
	googleEmail, _ := payload.Claims["email"].(string)
	emailVerified, _ := payload.Claims["email_verified"].(bool)
	firstName, _ := payload.Claims["given_name"].(string)
	lastName, _ := payload.Claims["family_name"].(string)
	googleSub := payload.Subject
	picture, _ := payload.Claims["picture"].(string)

	if googleEmail == "" || !emailVerified {
		response.BadRequest(c, "Google account email is not verified")
		return
	}

	// Check if user exists
	if input.TenantID != "" {
		// Tenant-specific lookup (multi-tenant selection)
		tenantUUID, err := uuid.Parse(input.TenantID)
		if err != nil {
			response.BadRequest(c, "Invalid tenant ID")
			return
		}
		h.googleLoginExistingUser(c, googleEmail, googleSub, picture, &tenantUUID)
		return
	}

	// Count users with this email
	var userCount int
	err = h.db.QueryRow("SELECT COUNT(*) FROM users WHERE email = $1 AND deleted_at IS NULL", googleEmail).Scan(&userCount)
	if err != nil {
		h.log.Error("Failed to count users", "error", err)
		response.InternalServerError(c, "")
		return
	}

	if userCount > 1 {
		// Multi-tenant: return tenant list for selection
		rows, err := h.db.Query(`
			SELECT t.id, t.name, t.code
			FROM users u
			JOIN tenants t ON u.tenant_id = t.id
			WHERE u.email = $1 AND u.deleted_at IS NULL AND t.is_active = true
		`, googleEmail)
		if err != nil {
			h.log.Error("Failed to query tenants", "error", err)
			response.InternalServerError(c, "")
			return
		}
		defer rows.Close()

		var tenants []TenantInfo
		for rows.Next() {
			var t TenantInfo
			rows.Scan(&t.ID, &t.Name, &t.Code)
			tenants = append(tenants, t)
		}

		c.JSON(http.StatusConflict, gin.H{
			"success": false,
			"data": gin.H{
				"tenants": tenants,
				"message": "Please select a company to continue",
			},
		})
		return
	}

	if userCount == 1 {
		// Single user found — login
		h.googleLoginExistingUser(c, googleEmail, googleSub, picture, nil)
		return
	}

	// No user found — new registration
	if input.CompanyName == "" {
		// Need company name — return needs_completion
		response.Success(c, GoogleAuthNeedsCompletionResponse{
			NeedsCompletion: true,
			GoogleUser: GoogleUserPreview{
				Email:     googleEmail,
				FirstName: firstName,
				LastName:  lastName,
				Picture:   picture,
			},
		})
		return
	}

	// Complete registration with Google
	h.googleRegisterNewUser(c, googleEmail, googleSub, firstName, lastName, picture, input.CompanyName)
}

// googleLoginExistingUser logs in an existing user via Google
func (h *Handler) googleLoginExistingUser(c *gin.Context, email, googleSub, picture string, tenantID *uuid.UUID) {
	query := `
		SELECT id, tenant_id, COALESCE(email, ''), first_name, last_name,
		       phone, avatar_url, language, timezone, is_active, is_verified,
		       is_system_admin, COALESCE(auth_provider, 'local') as auth_provider, google_id,
		       created_at, updated_at
		FROM users
		WHERE email = $1 AND deleted_at IS NULL
	`
	args := []interface{}{email}

	if tenantID != nil {
		query = `
			SELECT id, tenant_id, COALESCE(email, ''), first_name, last_name,
			       phone, avatar_url, language, timezone, is_active, is_verified,
			       is_system_admin, COALESCE(auth_provider, 'local') as auth_provider, google_id,
			       created_at, updated_at
			FROM users
			WHERE tenant_id = $1 AND email = $2 AND deleted_at IS NULL
		`
		args = []interface{}{*tenantID, email}
	}

	var user entity.User
	var phone, avatarURL, googleID sql.NullString

	err := h.db.QueryRow(query, args...).Scan(
		&user.ID, &user.TenantID, &user.Email, &user.FirstName, &user.LastName,
		&phone, &avatarURL, &user.Language, &user.Timezone, &user.IsActive, &user.IsVerified,
		&user.IsSystemAdmin, &user.AuthProvider, &googleID,
		&user.CreatedAt, &user.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.Error(c, http.StatusUnauthorized, response.ErrCodeInvalidCredentials, "Account not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to query user for Google auth", "error", err)
		response.InternalServerError(c, "")
		return
	}

	if phone.Valid {
		user.Phone = &phone.String
	}
	if avatarURL.Valid {
		user.AvatarURL = &avatarURL.String
	}
	if googleID.Valid {
		user.GoogleID = &googleID.String
	}

	if !user.IsActive {
		response.Error(c, http.StatusUnauthorized, response.ErrCodeAccountDisabled, "Account is disabled")
		return
	}

	// Link Google ID if not already linked, and update avatar if missing
	if !googleID.Valid || googleID.String == "" {
		h.db.Exec("UPDATE users SET google_id = $1, auth_provider = CASE WHEN auth_provider = 'local' AND password_hash IS NULL THEN 'google' ELSE auth_provider END WHERE id = $2", googleSub, user.ID)
	}
	if (user.AvatarURL == nil || *user.AvatarURL == "") && picture != "" {
		h.db.Exec("UPDATE users SET avatar_url = $1 WHERE id = $2", picture, user.ID)
		user.AvatarURL = &picture
	}

	// Update last login
	now := time.Now()
	h.db.Exec("UPDATE users SET last_login_at = $1 WHERE id = $2", now, user.ID)
	user.LastLoginAt = &now

	// Generate tokens
	tokenPair, err := h.jwtManager.GenerateTokenPair(user.ID, user.TenantID, user.Email, user.IsSystemAdmin)
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
	`, user.ID, tokenHash, deviceInfo, c.ClientIP(), tokenPair.ExpiresAt.Add(h.config.JWT.RefreshTokenExpiry))

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

	// Get tenant info
	var tenantCode, tenantName string
	h.db.QueryRow("SELECT code, name FROM tenants WHERE id = $1", user.TenantID).Scan(&tenantCode, &tenantName)

	response.Success(c, AuthResponse{
		User: user.ToResponse(),
		Tenant: &TenantInfo{
			ID:   user.TenantID,
			Code: tenantCode,
			Name: tenantName,
		},
		AccessToken:  tokenPair.AccessToken,
		RefreshToken: tokenPair.RefreshToken,
		ExpiresAt:    tokenPair.ExpiresAt,
		TokenType:    tokenPair.TokenType,
	})
}

// googleRegisterNewUser creates a new tenant and user from Google sign-up
func (h *Handler) googleRegisterNewUser(c *gin.Context, email, googleSub, firstName, lastName, picture, companyName string) {
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to begin transaction", "error", err)
		response.InternalServerError(c, "")
		return
	}
	defer tx.Rollback()

	// Double-check email doesn't exist
	var existingID uuid.UUID
	err = tx.QueryRow("SELECT id FROM users WHERE email = $1 AND deleted_at IS NULL", email).Scan(&existingID)
	if err == nil {
		response.Conflict(c, "Email already registered")
		return
	}
	if err != sql.ErrNoRows {
		h.log.Error("Failed to check email", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Generate tenant code (supports Cyrillic/non-Latin company names)
	tenantCode := generateTenantCode(companyName, randomSuffix)

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
	`, tenantID, tenantCode, companyName, defaultSettings)
	if err != nil {
		h.log.Error("Failed to create tenant", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Create user (no password, Google auth)
	userID := uuid.New()
	now := time.Now()
	defaultUserSettings, _ := json.Marshal(map[string]interface{}{})

	_, err = tx.Exec(`
		INSERT INTO users (id, tenant_id, email, first_name, last_name, settings,
		                   is_active, is_verified, is_system_admin, auth_provider, google_id,
		                   avatar_url, role, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, true, true, false, 'google', $7, $8, 'owner', $9, $9)
	`, userID, tenantID, email, firstName, lastName, defaultUserSettings, googleSub, picture, now)
	if err != nil {
		h.log.Error("Failed to create user", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Create owner role
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

	_, err = tx.Exec("INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)", userID, roleID)
	if err != nil {
		h.log.Error("Failed to assign role", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Create default warehouse
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

	// Seed default units of measure
	h.seedDefaultUnitsOfMeasure(tx, tenantID)

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Generate tokens
	tokenPair, err := h.jwtManager.GenerateTokenPair(userID, tenantID, email, false)
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

	ownerRoleDesc := "Tenant owner with full access"
	var avatarPtr *string
	if picture != "" {
		avatarPtr = &picture
	}

	userResp := &entity.UserResponse{
		ID:            userID,
		TenantID:      tenantID,
		Email:         email,
		FirstName:     firstName,
		LastName:      lastName,
		FullName:      firstName + " " + lastName,
		AvatarURL:     avatarPtr,
		Language:      "en",
		Timezone:      "UTC",
		IsActive:      true,
		IsVerified:    true,
		IsSystemAdmin: false,
		AuthProvider:  "google",
		HasPassword:   false,
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

	response.Created(c, gin.H{
		"user": userResp,
		"tenant": &TenantInfo{
			ID:   tenantID,
			Code: tenantCode,
			Name: companyName,
		},
		"access_token":  tokenPair.AccessToken,
		"refresh_token": tokenPair.RefreshToken,
		"expires_at":    tokenPair.ExpiresAt,
		"token_type":    tokenPair.TokenType,
		"is_new_user":   true,
	})
}

func randomSuffix() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 6)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}

// seedDefaultUnitsOfMeasure creates standard UoM entries for a new tenant
func (h *Handler) seedDefaultUnitsOfMeasure(tx *sql.Tx, tenantID uuid.UUID) {
	units := []struct {
		code, name, category string
		factor               float64
	}{
		{"unit", "Dona", "quantity", 1},
		{"kg", "Kilogramm", "weight", 1},
		{"g", "Gramm", "weight", 0.001},
		{"l", "Litr", "volume", 1},
		{"ml", "Millilitr", "volume", 0.001},
		{"m", "Metr", "length", 1},
		{"cm", "Santimetr", "length", 0.01},
		{"box", "Quti", "quantity", 1},
		{"pack", "Paket", "quantity", 1},
		{"dozen", "Dujina", "quantity", 12},
		{"t", "Tonna", "weight", 1000},
		{"pcs", "Dona (pcs)", "quantity", 1},
		{"set", "To'plam", "quantity", 1},
		{"pair", "Juft", "quantity", 2},
		{"roll", "Rulon", "quantity", 1},
		{"sheet", "Varaq", "quantity", 1},
		{"m2", "Kvadrat metr", "area", 1},
		{"m3", "Kub metr", "volume", 1000},
	}
	for _, u := range units {
		_, err := tx.Exec(`
			INSERT INTO units_of_measure (id, tenant_id, code, name, category, conversion_factor, is_active)
			VALUES ($1, $2, $3, $4, $5, $6, true)
			ON CONFLICT DO NOTHING
		`, uuid.New(), tenantID, u.code, u.name, u.category, u.factor)
		if err != nil {
			h.log.Error("Failed to seed UoM", "error", err, "code", u.code)
		}
	}
}

// UpdateCurrentUserEmail updates email without verification
func (h *Handler) UpdateCurrentUserEmail(c *gin.Context) {
	claims, ok := middleware.GetClaims(c)
	if !ok {
		response.Unauthorized(c, "")
		return
	}

	var input struct {
		Email string `json:"email" binding:"required,email"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid email")
		return
	}

	_, err := h.db.Exec("UPDATE users SET email = $1, updated_at = NOW() WHERE id = $2", input.Email, claims.UserID)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			response.BadRequest(c, "This email is already in use")
			return
		}
		h.log.Error("Failed to update email", "error", err)
		response.InternalServerError(c, "Failed to update email")
		return
	}

	// Sync email to linked employee record
	h.db.Exec("UPDATE employees SET email = $1, updated_at = NOW() WHERE user_id = $2 AND deleted_at IS NULL", input.Email, claims.UserID)

	h.GetCurrentUser(c)
}

// SendPhoneOTP sends a 6-digit OTP code via SMS for phone number verification
func (h *Handler) SendPhoneOTP(c *gin.Context) {
	claims, ok := middleware.GetClaims(c)
	if !ok {
		response.Unauthorized(c, "")
		return
	}

	var input struct {
		Phone string `json:"phone" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Phone number is required")
		return
	}

	// Generate OTP
	otpCode := generateOTPCode()
	expiresAt := time.Now().Add(5 * time.Minute)

	// Invalidate previous phone OTPs for this user
	h.db.Exec(`
		UPDATE email_verification_otps SET verified_at = NOW()
		WHERE email = $1 AND purpose = 'phone_change' AND verified_at IS NULL
	`, claims.UserID.String())

	// Store OTP (reuse email_verification_otps table, use userID as "email" key)
	_, err := h.db.Exec(`
		INSERT INTO email_verification_otps (email, otp_code, purpose, expires_at)
		VALUES ($1, $2, 'phone_change', $3)
	`, claims.UserID.String()+":"+input.Phone, otpCode, expiresAt)
	if err != nil {
		h.log.Error("Failed to store phone OTP", "error", err)
		response.InternalServerError(c, "Failed to send OTP")
		return
	}

	// Send SMS
	smsMessage := fmt.Sprintf("Genix ERP tasdiqlash kodi: %s. Kodni hech kimga bermang.", otpCode)
	if err := h.smsService.Send(input.Phone, smsMessage); err != nil {
		h.log.Error("Failed to send SMS OTP", "error", err, "phone", input.Phone)
		// Don't fail — in dev mode SMS logs to console
	}

	result := gin.H{
		"message": "OTP code sent",
		"phone":   input.Phone,
	}

	// In development, include OTP for testing
	if h.config.App.Env == "development" || h.config.App.Env == "local" || h.config.App.Env == "" {
		result["dev_otp_code"] = otpCode
	}

	response.Success(c, result)
}

// VerifyPhoneOTP verifies the OTP and updates the user's phone number
func (h *Handler) VerifyPhoneOTP(c *gin.Context) {
	claims, ok := middleware.GetClaims(c)
	if !ok {
		response.Unauthorized(c, "")
		return
	}

	var input struct {
		Phone   string `json:"phone" binding:"required"`
		OTPCode string `json:"otp_code" binding:"required,len=6"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Phone and OTP code are required")
		return
	}

	// Look up the OTP
	var otpID uuid.UUID
	var storedCode string
	var expiresAt time.Time
	var verifiedAt sql.NullTime

	lookupKey := claims.UserID.String() + ":" + input.Phone
	err := h.db.QueryRow(`
		SELECT id, otp_code, expires_at, verified_at
		FROM email_verification_otps
		WHERE email = $1 AND purpose = 'phone_change'
		ORDER BY created_at DESC LIMIT 1
	`, lookupKey).Scan(&otpID, &storedCode, &expiresAt, &verifiedAt)

	if err == sql.ErrNoRows {
		response.BadRequest(c, "No OTP found. Please request a new one.")
		return
	}
	if err != nil {
		h.log.Error("Failed to query phone OTP", "error", err)
		response.InternalServerError(c, "")
		return
	}

	if verifiedAt.Valid {
		response.BadRequest(c, "OTP already used. Please request a new one.")
		return
	}
	if time.Now().After(expiresAt) {
		response.BadRequest(c, "OTP has expired. Please request a new one.")
		return
	}
	if storedCode != input.OTPCode {
		h.db.Exec("UPDATE email_verification_otps SET attempts = attempts + 1 WHERE id = $1", otpID)
		response.BadRequest(c, "Invalid OTP code")
		return
	}

	// Mark OTP as verified
	h.db.Exec("UPDATE email_verification_otps SET verified_at = NOW() WHERE id = $1", otpID)

	// Update the user's phone number
	_, err = h.db.Exec("UPDATE users SET phone = $1, updated_at = NOW() WHERE id = $2", input.Phone, claims.UserID)
	if err != nil {
		h.log.Error("Failed to update phone number", "error", err)
		response.InternalServerError(c, "Failed to update phone number")
		return
	}

	// Sync phone to linked employee record
	h.db.Exec("UPDATE employees SET phone = $1, updated_at = NOW() WHERE user_id = $2 AND deleted_at IS NULL", input.Phone, claims.UserID)

	// Return the updated user
	h.GetCurrentUser(c)
}
