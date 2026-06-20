package entity

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// User represents a user in the system
type User struct {
	ID                  uuid.UUID       `json:"id" db:"id"`
	TenantID            uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	Email               string          `json:"email" db:"email"`
	PasswordHash        string          `json:"-" db:"password_hash"`
	FirstName           string          `json:"first_name" db:"first_name"`
	LastName            string          `json:"last_name" db:"last_name"`
	Phone               *string         `json:"phone,omitempty" db:"phone"`
	AvatarURL           *string         `json:"avatar_url,omitempty" db:"avatar_url"`
	Language            string          `json:"language" db:"language"`
	Timezone            string          `json:"timezone" db:"timezone"`
	Settings            json.RawMessage `json:"settings" db:"settings" swaggertype:"object"`
	IsActive            bool            `json:"is_active" db:"is_active"`
	IsVerified          bool            `json:"is_verified" db:"is_verified"`
	IsSystemAdmin       bool            `json:"is_system_admin" db:"is_system_admin"`
	LastLoginAt         *time.Time      `json:"last_login_at,omitempty" db:"last_login_at"`
	PasswordChangedAt   *time.Time      `json:"password_changed_at,omitempty" db:"password_changed_at"`
	AuthProvider        string          `json:"auth_provider" db:"auth_provider"`
	GoogleID            *string         `json:"google_id,omitempty" db:"google_id"`
	FailedLoginAttempts int             `json:"-" db:"failed_login_attempts"`
	LockedUntil         *time.Time      `json:"-" db:"locked_until"`
	InviteTokenExpires  *time.Time      `json:"invite_token_expires,omitempty" db:"invite_token_expires"`
	CreatedAt           time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at" db:"updated_at"`
	DeletedAt           sql.NullTime    `json:"-" db:"deleted_at"`

	// Relationships (loaded separately)
	Roles       []Role       `json:"roles,omitempty"`
	Permissions []Permission `json:"permissions,omitempty"`
}

// FullName returns the user's full name
func (u *User) FullName() string {
	return u.FirstName + " " + u.LastName
}

// IsLocked checks if the user account is locked
func (u *User) IsLocked() bool {
	if u.LockedUntil == nil {
		return false
	}
	return time.Now().Before(*u.LockedUntil)
}

// Role represents a role in the RBAC system
type Role struct {
	ID          uuid.UUID       `json:"id" db:"id"`
	TenantID    uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	Name        string          `json:"name" db:"name"`
	Code        string          `json:"code" db:"code"`
	Description *string         `json:"description,omitempty" db:"description"`
	IsSystem    bool            `json:"is_system" db:"is_system"`
	Permissions json.RawMessage `json:"permissions" db:"permissions" swaggertype:"object"`
	CreatedAt   time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at" db:"updated_at"`
}

// Permission represents a permission in the RBAC system
type Permission struct {
	ID          uuid.UUID `json:"id" db:"id"`
	Module      string    `json:"module" db:"module"`
	Resource    string    `json:"resource" db:"resource"`
	Action      string    `json:"action" db:"action"`
	Description *string   `json:"description,omitempty" db:"description"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}

// RefreshToken represents a refresh token for JWT authentication
type RefreshToken struct {
	ID         uuid.UUID       `json:"id" db:"id"`
	UserID     uuid.UUID       `json:"user_id" db:"user_id"`
	TokenHash  string          `json:"-" db:"token_hash"`
	DeviceInfo json.RawMessage `json:"device_info,omitempty" db:"device_info" swaggertype:"object"`
	IPAddress  *string         `json:"ip_address,omitempty" db:"ip_address"`
	ExpiresAt  time.Time       `json:"expires_at" db:"expires_at"`
	CreatedAt  time.Time       `json:"created_at" db:"created_at"`
	RevokedAt  *time.Time      `json:"revoked_at,omitempty" db:"revoked_at"`
}

// APIKey represents an API key for programmatic access
type APIKey struct {
	ID         uuid.UUID       `json:"id" db:"id"`
	TenantID   uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	UserID     *uuid.UUID      `json:"user_id,omitempty" db:"user_id"`
	Name       string          `json:"name" db:"name"`
	KeyHash    string          `json:"-" db:"key_hash"`
	KeyPrefix  string          `json:"key_prefix" db:"key_prefix"`
	Scopes     json.RawMessage `json:"scopes" db:"scopes" swaggertype:"object"`
	RateLimit  int             `json:"rate_limit" db:"rate_limit"`
	ExpiresAt  *time.Time      `json:"expires_at,omitempty" db:"expires_at"`
	LastUsedAt *time.Time      `json:"last_used_at,omitempty" db:"last_used_at"`
	IsActive   bool            `json:"is_active" db:"is_active"`
	CreatedAt  time.Time       `json:"created_at" db:"created_at"`
	RevokedAt  *time.Time      `json:"revoked_at,omitempty" db:"revoked_at"`
}

// UserSettings represents user preferences
type UserSettings struct {
	Theme            string `json:"theme"`
	DashboardLayout  string `json:"dashboard_layout"`
	DefaultView      string `json:"default_view"`
	EmailNotifications bool `json:"email_notifications"`
	PushNotifications bool `json:"push_notifications"`
}

// CreateUserInput represents input for creating a user
type CreateUserInput struct {
	Email     string   `json:"email" binding:"omitempty,email"`
	Password  string   `json:"password" binding:"-"` // Optional - if not provided, user must be invited
	FirstName string   `json:"first_name" binding:"required,min=1,max=100"`
	LastName  string   `json:"last_name" binding:"required,min=1,max=100"`
	Phone     string   `json:"phone" binding:"-"`
	Language  string   `json:"language" binding:"-"`
	Timezone  string   `json:"timezone" binding:"-"`
	RoleIDs   []string `json:"role_ids" binding:"-"`
}

// UpdateUserInput represents input for updating a user
type UpdateUserInput struct {
	Email     *string  `json:"email,omitempty" binding:"omitempty,email"`
	FirstName *string  `json:"first_name,omitempty" binding:"omitempty,min=1,max=100"`
	LastName  *string  `json:"last_name,omitempty" binding:"omitempty,min=1,max=100"`
	Phone     *string  `json:"phone,omitempty"`
	AvatarURL *string  `json:"avatar_url,omitempty"`
	Language  *string  `json:"language,omitempty"`
	Timezone  *string  `json:"timezone,omitempty"`
	IsActive  *bool    `json:"is_active,omitempty"`
	RoleIDs   []string `json:"role_ids,omitempty"`
}

// ChangePasswordInput represents input for changing password
type ChangePasswordInput struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required,min=8"`
}

// CreateRoleInput represents input for creating a role
type CreateRoleInput struct {
	Name          string   `json:"name" binding:"required,min=2,max=100"`
	Code          string   `json:"code" binding:"required,min=2,max=50"`
	Description   string   `json:"description,omitempty"`
	PermissionIDs []string `json:"permission_ids,omitempty"`
}

// UpdateRoleInput represents input for updating a role
type UpdateRoleInput struct {
	Name          *string  `json:"name,omitempty"`
	Description   *string  `json:"description,omitempty"`
	PermissionIDs []string `json:"permission_ids,omitempty"`
}

// AssignRoleInput represents input for assigning roles to a user
type AssignRoleInput struct {
	UserID  string   `json:"user_id" binding:"required"`
	RoleIDs []string `json:"role_ids" binding:"required"`
}

// UserListFilter represents filters for listing users
type UserListFilter struct {
	Search   string `form:"search"`
	IsActive *bool  `form:"is_active"`
	RoleID   string `form:"role_id"`
}

// UserResponse represents the API response for a user
type UserResponse struct {
	ID                 uuid.UUID  `json:"id"`
	TenantID           uuid.UUID  `json:"tenant_id"`
	Email              string     `json:"email"`
	FirstName          string     `json:"first_name"`
	LastName           string     `json:"last_name"`
	FullName           string     `json:"full_name"`
	Phone              *string    `json:"phone,omitempty"`
	AvatarURL          *string    `json:"avatar_url,omitempty"`
	Language           string     `json:"language"`
	Timezone           string     `json:"timezone"`
	IsActive           bool       `json:"is_active"`
	IsVerified         bool       `json:"is_verified"`
	IsSystemAdmin      bool       `json:"is_system_admin"`
	AuthProvider       string     `json:"auth_provider"`
	HasPassword        bool       `json:"has_password"`
	InviteTokenExpires *time.Time `json:"invite_token_expires,omitempty"`
	LastLoginAt        *time.Time `json:"last_login_at,omitempty"`
	Roles              []Role     `json:"roles,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// HasPassword checks if the user has a password set
func (u *User) HasPassword() bool {
	return u.PasswordHash != ""
}

// ToResponse converts User to UserResponse
func (u *User) ToResponse() *UserResponse {
	return &UserResponse{
		ID:                 u.ID,
		TenantID:           u.TenantID,
		Email:              u.Email,
		FirstName:          u.FirstName,
		LastName:           u.LastName,
		FullName:           u.FullName(),
		Phone:              u.Phone,
		AvatarURL:          u.AvatarURL,
		Language:           u.Language,
		Timezone:           u.Timezone,
		IsActive:           u.IsActive,
		IsVerified:         u.IsVerified,
		IsSystemAdmin:      u.IsSystemAdmin,
		AuthProvider:       u.AuthProvider,
		HasPassword:        u.HasPassword(),
		InviteTokenExpires: u.InviteTokenExpires,
		LastLoginAt:        u.LastLoginAt,
		Roles:              u.Roles,
		CreatedAt:          u.CreatedAt,
		UpdatedAt:          u.UpdatedAt,
	}
}
