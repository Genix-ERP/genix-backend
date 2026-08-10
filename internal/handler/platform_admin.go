package handler

import (
	"database/sql"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/crypto"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ============================================================================
// PLATFORM AUTH  — separate control-plane login (Phase 3)
// ============================================================================

// PlatformLogin authenticates a platform staff user against platform_users and
// issues a platform-scoped token pair. POST /platform/auth/login (public).
func (h *Handler) PlatformLogin(c *gin.Context) {
	var input struct {
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required"`
		TOTPCode string `json:"totp_code"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	var (
		id           uuid.UUID
		hash, role   string
		first, last  string
		isActive     bool
		totpEnabled  bool
		totpSecret   sql.NullString
	)
	err := h.db.QueryRow(`
		SELECT id, password_hash, role, first_name, last_name, is_active, totp_enabled, totp_secret
		FROM platform_users
		WHERE lower(email) = lower($1) AND deleted_at IS NULL
	`, input.Email).Scan(&id, &hash, &role, &first, &last, &isActive, &totpEnabled, &totpSecret)
	if err == sql.ErrNoRows {
		response.Unauthorized(c, "Invalid credentials")
		return
	}
	if err != nil {
		h.log.Error("platform login lookup failed", "error", err)
		response.InternalServerError(c, "")
		return
	}
	if !isActive {
		response.Forbidden(c, "Account is disabled")
		return
	}

	ok, err := crypto.VerifyPassword(input.Password, hash)
	if err != nil || !ok {
		response.Unauthorized(c, "Invalid credentials")
		return
	}

	// TOTP step-up when enrolled. (Enrollment endpoint TODO; enforcement is here
	// so a platform user with 2FA on cannot log in without the code.)
	if totpEnabled && totpSecret.Valid && totpSecret.String != "" {
		if input.TOTPCode == "" || !crypto.VerifyTOTP(totpSecret.String, input.TOTPCode) {
			response.Unauthorized(c, "Valid 2FA code required")
			return
		}
	}

	pair, err := h.jwtManager.GeneratePlatformTokenPair(id, input.Email, role)
	if err != nil {
		h.log.Error("platform token mint failed", "error", err)
		response.InternalServerError(c, "")
		return
	}
	h.db.Exec(`UPDATE platform_users SET last_login_at = NOW() WHERE id = $1`, id)
	h.writePlatformAudit(c, "platform.login", "platform_user", id.String(), nil, nil,
		map[string]interface{}{"email": input.Email, "role": role})

	response.Success(c, gin.H{
		"access_token":  pair.AccessToken,
		"refresh_token": pair.RefreshToken,
		"expires_at":    pair.ExpiresAt,
		"token_type":    "Bearer",
		"platform_user": gin.H{"id": id, "email": input.Email, "first_name": first, "last_name": last, "role": role},
	})
}

// GetPlatformMe returns the current platform user + their capabilities.
// GET /admin/platform/me (RequireSystemAdmin).
func (h *Handler) GetPlatformMe(c *gin.Context) {
	claims, _ := middleware.GetClaims(c)
	role := middleware.EffectivePlatformRole(claims)
	out := gin.H{"role": role, "capabilities": middleware.CapabilitiesForRole(role)}
	if claims != nil && claims.PlatformUserID != nil {
		var email, first, last string
		if err := h.db.QueryRow(`SELECT email, first_name, last_name FROM platform_users WHERE id = $1`, *claims.PlatformUserID).
			Scan(&email, &first, &last); err == nil {
			out["id"] = *claims.PlatformUserID
			out["email"] = email
			out["first_name"] = first
			out["last_name"] = last
		}
	} else if claims != nil {
		out["email"] = claims.Email
		out["legacy"] = true // tenant-admin token acting as super_admin
	}
	response.Success(c, out)
}

// GetRoleMatrix returns the fixed capability matrix for the read-only
// Rollar/Ruxsatlar screen. GET /admin/role-matrix.
func (h *Handler) GetRoleMatrix(c *gin.Context) {
	roles := []string{
		middleware.PlatformRoleSuperAdmin, middleware.PlatformRoleAdmin,
		middleware.PlatformRoleManejer, middleware.PlatformRoleTexSupport,
	}
	matrix := make(map[string]map[string]bool, len(roles))
	for _, r := range roles {
		matrix[r] = middleware.CapabilitiesForRole(r)
	}
	response.Success(c, gin.H{"roles": roles, "matrix": matrix})
}

// ============================================================================
// PLATFORM USER MANAGEMENT  (super_admin only)
// ============================================================================

func (h *Handler) ListPlatformUsers(c *gin.Context) {
	rows, err := h.db.Query(`
		SELECT id, email, first_name, last_name, role, is_active, totp_enabled, last_login_at, created_at
		FROM platform_users WHERE deleted_at IS NULL ORDER BY created_at ASC
	`)
	if err != nil {
		response.InternalServerError(c, "")
		return
	}
	defer rows.Close()
	type Row struct {
		ID          uuid.UUID  `json:"id"`
		Email       string     `json:"email"`
		FirstName   string     `json:"first_name"`
		LastName    string     `json:"last_name"`
		Role        string     `json:"role"`
		IsActive    bool       `json:"is_active"`
		TOTPEnabled bool       `json:"totp_enabled"`
		LastLoginAt *time.Time `json:"last_login_at,omitempty"`
		CreatedAt   time.Time  `json:"created_at"`
	}
	var out []Row
	for rows.Next() {
		var r Row
		var ll sql.NullTime
		if err := rows.Scan(&r.ID, &r.Email, &r.FirstName, &r.LastName, &r.Role, &r.IsActive, &r.TOTPEnabled, &ll, &r.CreatedAt); err != nil {
			continue
		}
		if ll.Valid {
			r.LastLoginAt = &ll.Time
		}
		out = append(out, r)
	}
	response.Success(c, out)
}

func validPlatformRole(role string) bool {
	switch role {
	case middleware.PlatformRoleSuperAdmin, middleware.PlatformRoleAdmin,
		middleware.PlatformRoleManejer, middleware.PlatformRoleTexSupport:
		return true
	}
	return false
}

func (h *Handler) CreatePlatformUser(c *gin.Context) {
	var in struct {
		Email     string `json:"email" binding:"required,email"`
		Password  string `json:"password" binding:"required,min=8"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Role      string `json:"role" binding:"required"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	if !validPlatformRole(in.Role) {
		response.BadRequest(c, "Invalid role")
		return
	}
	hash, err := crypto.HashPassword(in.Password)
	if err != nil {
		response.InternalServerError(c, "")
		return
	}
	var id uuid.UUID
	err = h.db.QueryRow(`
		INSERT INTO platform_users (email, password_hash, first_name, last_name, role)
		VALUES ($1, $2, $3, $4, $5) RETURNING id
	`, in.Email, hash, in.FirstName, in.LastName, in.Role).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") {
			response.Conflict(c, "A platform user with this email already exists")
			return
		}
		h.log.Error("create platform user failed", "error", err)
		response.InternalServerError(c, "")
		return
	}
	h.writePlatformAudit(c, "platform_user.create", "platform_user", id.String(), nil, nil,
		map[string]interface{}{"email": in.Email, "role": in.Role})
	response.Created(c, gin.H{"id": id, "email": in.Email, "role": in.Role})
}

func (h *Handler) UpdatePlatformUser(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid id")
		return
	}
	var in struct {
		Role     *string `json:"role"`
		IsActive *bool   `json:"is_active"`
		Password *string `json:"password"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	if in.Role != nil {
		if !validPlatformRole(*in.Role) {
			response.BadRequest(c, "Invalid role")
			return
		}
		h.db.Exec(`UPDATE platform_users SET role = $1, updated_at = NOW() WHERE id = $2`, *in.Role, id)
	}
	if in.IsActive != nil {
		h.db.Exec(`UPDATE platform_users SET is_active = $1, updated_at = NOW() WHERE id = $2`, *in.IsActive, id)
	}
	if in.Password != nil && len(*in.Password) >= 8 {
		if hash, err := crypto.HashPassword(*in.Password); err == nil {
			h.db.Exec(`UPDATE platform_users SET password_hash = $1, updated_at = NOW() WHERE id = $2`, hash, id)
		}
	}
	h.writePlatformAudit(c, "platform_user.update", "platform_user", id.String(), nil, nil,
		map[string]interface{}{"role": in.Role, "is_active": in.IsActive})
	response.Success(c, gin.H{"id": id, "updated": true})
}

func (h *Handler) DeletePlatformUser(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid id")
		return
	}
	// Guard: never remove the last active super_admin.
	var superCount int
	h.db.QueryRow(`SELECT COUNT(*) FROM platform_users WHERE role = 'super_admin' AND is_active = true AND deleted_at IS NULL`).Scan(&superCount)
	var targetRole string
	h.db.QueryRow(`SELECT role FROM platform_users WHERE id = $1 AND deleted_at IS NULL`, id).Scan(&targetRole)
	if targetRole == middleware.PlatformRoleSuperAdmin && superCount <= 1 {
		response.BadRequest(c, "Cannot remove the last super admin")
		return
	}
	res, _ := h.db.Exec(`UPDATE platform_users SET deleted_at = NOW(), is_active = false WHERE id = $1 AND deleted_at IS NULL`, id)
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "Platform user")
		return
	}
	h.writePlatformAudit(c, "platform_user.delete", "platform_user", id.String(), nil, nil, nil)
	response.Success(c, gin.H{"deleted": true})
}
