package handler

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/crypto"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TenderRegister handles tender platform user registration
func (h *Handler) TenderRegister(c *gin.Context) {
	var input entity.TenderRegisterInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Noto'g'ri ma'lumotlar: "+err.Error())
		return
	}

	// Check if email already exists
	var exists bool
	err := h.db.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM tender_users WHERE email = $1)", input.Email).Scan(&exists)
	if err != nil {
		h.log.Error("Failed to check existing email", "error", err)
		response.InternalServerError(c, "")
		return
	}
	if exists {
		response.Conflict(c, "Bu email allaqachon ro'yxatdan o'tgan")
		return
	}

	// Hash password
	hashedPassword, err := crypto.HashPassword(input.Password)
	if err != nil {
		h.log.Error("Failed to hash password", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Insert user
	var user entity.TenderUser
	err = h.db.DB.QueryRow(`
		INSERT INTO tender_users (email, password_hash, full_name, phone, role, company_name, inn, region_id)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, NULLIF($6, ''), NULLIF($7, ''), $8)
		RETURNING id, email, password_hash, full_name, phone, role, company_name, inn, region_id,
		          is_active, is_verified, last_login_at, created_at, updated_at
	`, input.Email, hashedPassword, input.FullName, input.Phone, input.Role,
		input.CompanyName, input.INN, input.RegionID,
	).Scan(
		&user.ID, &user.Email, &user.PasswordHash, &user.FullName, &user.Phone,
		&user.Role, &user.CompanyName, &user.INN, &user.RegionID,
		&user.IsActive, &user.IsVerified, &user.LastLoginAt, &user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		h.log.Error("Failed to create tender user", "error", err)
		response.InternalServerError(c, "Foydalanuvchini yaratib bo'lmadi")
		return
	}

	// Auto-create company profile
	_, err = h.db.DB.Exec(`
		INSERT INTO tender_company_profiles (id, user_id, role, company_name, inn, phone, region_id)
		VALUES ($1, $2, $3, COALESCE(NULLIF($4, ''), $5), COALESCE(NULLIF($6, ''), ''), COALESCE(NULLIF($7, ''), ''), $8)
	`, user.ID, user.ID, input.Role,
		input.CompanyName, input.FullName,
		input.INN, input.Phone, input.RegionID)
	if err != nil {
		h.log.Error("Failed to create company profile", "error", err)
	}

	// Generate JWT tokens (use uuid.Nil for TenantID since tender has no tenants)
	tokens, err := h.jwtManager.GenerateTokenPair(user.ID, uuid.Nil, user.Email, false)
	if err != nil {
		h.log.Error("Failed to generate tokens", "error", err)
		response.InternalServerError(c, "")
		return
	}

	c.JSON(http.StatusCreated, response.Response{
		Success: true,
		Data: gin.H{
			"user":          user.ToResponse(),
			"access_token":  tokens.AccessToken,
			"refresh_token": tokens.RefreshToken,
			"expires_at":    tokens.ExpiresAt,
			"token_type":    tokens.TokenType,
		},
	})
}

// TenderLogin handles tender platform user login
func (h *Handler) TenderLogin(c *gin.Context) {
	var input entity.TenderLoginInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Noto'g'ri ma'lumotlar")
		return
	}

	// Find user by email
	var user entity.TenderUser
	err := h.db.DB.QueryRow(`
		SELECT id, email, password_hash, full_name, phone, role, company_name, inn, region_id,
		       is_active, is_verified, last_login_at, created_at, updated_at
		FROM tender_users WHERE email = $1
	`, input.Email).Scan(
		&user.ID, &user.Email, &user.PasswordHash, &user.FullName, &user.Phone,
		&user.Role, &user.CompanyName, &user.INN, &user.RegionID,
		&user.IsActive, &user.IsVerified, &user.LastLoginAt, &user.CreatedAt, &user.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		response.Error(c, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Email yoki parol noto'g'ri")
		return
	}
	if err != nil {
		h.log.Error("Failed to query tender user", "error", err)
		response.InternalServerError(c, "")
		return
	}

	if !user.IsActive {
		response.Error(c, http.StatusForbidden, "ACCOUNT_DISABLED", "Hisob bloklangan")
		return
	}

	// Verify password
	match, err := crypto.VerifyPassword(input.Password, user.PasswordHash)
	if err != nil || !match {
		response.Error(c, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Email yoki parol noto'g'ri")
		return
	}

	// Update last login
	now := time.Now()
	_, _ = h.db.DB.Exec("UPDATE tender_users SET last_login_at = $1 WHERE id = $2", now, user.ID)

	// Generate JWT tokens
	tokens, err := h.jwtManager.GenerateTokenPair(user.ID, uuid.Nil, user.Email, false)
	if err != nil {
		h.log.Error("Failed to generate tokens", "error", err)
		response.InternalServerError(c, "")
		return
	}

	response.Success(c, gin.H{
		"user":          user.ToResponse(),
		"access_token":  tokens.AccessToken,
		"refresh_token": tokens.RefreshToken,
		"expires_at":    tokens.ExpiresAt,
		"token_type":    tokens.TokenType,
	})
}

// TenderGetMe returns the current authenticated tender user's profile
func (h *Handler) TenderGetMe(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		response.Unauthorized(c, "")
		return
	}

	var user entity.TenderUser
	err := h.db.DB.QueryRow(`
		SELECT id, email, password_hash, full_name, phone, role, company_name, inn, region_id,
		       is_active, is_verified, last_login_at, created_at, updated_at
		FROM tender_users WHERE id = $1
	`, userID).Scan(
		&user.ID, &user.Email, &user.PasswordHash, &user.FullName, &user.Phone,
		&user.Role, &user.CompanyName, &user.INN, &user.RegionID,
		&user.IsActive, &user.IsVerified, &user.LastLoginAt, &user.CreatedAt, &user.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Foydalanuvchi topilmadi")
		return
	}
	if err != nil {
		h.log.Error("Failed to get tender user", "error", err)
		response.InternalServerError(c, "")
		return
	}

	response.Success(c, user.ToResponse())
}

// TenderUpdateMe updates the current authenticated tender user's profile
func (h *Handler) TenderUpdateMe(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		response.Unauthorized(c, "")
		return
	}

	var input struct {
		FullName    *string `json:"full_name"`
		Phone       *string `json:"phone"`
		CompanyName *string `json:"company_name"`
		INN         *string `json:"inn"`
		RegionID    *int    `json:"region_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Noto'g'ri ma'lumotlar")
		return
	}

	var user entity.TenderUser
	err := h.db.DB.QueryRow(`
		UPDATE tender_users SET
			full_name = COALESCE($2, full_name),
			phone = COALESCE($3, phone),
			company_name = COALESCE($4, company_name),
			inn = COALESCE($5, inn),
			region_id = COALESCE($6, region_id),
			updated_at = NOW()
		WHERE id = $1
		RETURNING id, email, password_hash, full_name, phone, role, company_name, inn, region_id,
		          is_active, is_verified, last_login_at, created_at, updated_at
	`, userID, input.FullName, input.Phone, input.CompanyName, input.INN, input.RegionID,
	).Scan(
		&user.ID, &user.Email, &user.PasswordHash, &user.FullName, &user.Phone,
		&user.Role, &user.CompanyName, &user.INN, &user.RegionID,
		&user.IsActive, &user.IsVerified, &user.LastLoginAt, &user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		h.log.Error("Failed to update tender user", "error", err)
		response.InternalServerError(c, "")
		return
	}

	response.Success(c, user.ToResponse())
}

// TenderRefreshToken handles token refresh for tender users
func (h *Handler) TenderRefreshToken(c *gin.Context) {
	var input struct {
		RefreshToken string `json:"refresh_token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "refresh_token majburiy")
		return
	}

	tokens, err := h.jwtManager.RefreshTokens(input.RefreshToken)
	if err != nil {
		response.Unauthorized(c, "Token eskirgan yoki noto'g'ri")
		return
	}

	response.Success(c, gin.H{
		"access_token":  tokens.AccessToken,
		"refresh_token": tokens.RefreshToken,
		"expires_at":    tokens.ExpiresAt,
		"token_type":    tokens.TokenType,
	})
}
