package entity

import (
	"time"

	"github.com/google/uuid"
)

// TenderUser represents a user on the tender platform (separate from ERP users)
type TenderUser struct {
	ID           uuid.UUID  `json:"id" db:"id"`
	Email        string     `json:"email" db:"email"`
	PasswordHash string     `json:"-" db:"password_hash"`
	FullName     string     `json:"full_name" db:"full_name"`
	Phone        *string    `json:"phone,omitempty" db:"phone"`
	Role         string     `json:"role" db:"role"`
	CompanyName  *string    `json:"company_name,omitempty" db:"company_name"`
	INN          *string    `json:"inn,omitempty" db:"inn"`
	RegionID     *uuid.UUID `json:"region_id,omitempty" db:"region_id"`
	IsActive     bool       `json:"is_active" db:"is_active"`
	IsVerified   bool       `json:"is_verified" db:"is_verified"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty" db:"last_login_at"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at" db:"updated_at"`
}

// TenderRegisterInput represents the registration form data
type TenderRegisterInput struct {
	Email       string `json:"email" binding:"required,email"`
	Password    string `json:"password" binding:"required,min=6"`
	FullName    string `json:"full_name" binding:"required,min=2,max=200"`
	Phone       string `json:"phone"`
	Role        string `json:"role" binding:"required,oneof=buyer supplier"`
	CompanyName string `json:"company_name"`
	INN         string `json:"inn"`
	RegionID    *uuid.UUID `json:"region_id"`
}

// TenderLoginInput represents the login form data
type TenderLoginInput struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

// TenderUserResponse is the safe response DTO for tender users
type TenderUserResponse struct {
	ID          uuid.UUID  `json:"id"`
	Email       string     `json:"email"`
	FullName    string     `json:"full_name"`
	Phone       *string    `json:"phone,omitempty"`
	Role        string     `json:"role"`
	CompanyName *string    `json:"company_name,omitempty"`
	INN         *string    `json:"inn,omitempty"`
	RegionID    *uuid.UUID `json:"region_id,omitempty"`
	IsActive    bool       `json:"is_active"`
	IsVerified  bool       `json:"is_verified"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// ToResponse converts TenderUser to its response DTO
func (u *TenderUser) ToResponse() *TenderUserResponse {
	return &TenderUserResponse{
		ID:          u.ID,
		Email:       u.Email,
		FullName:    u.FullName,
		Phone:       u.Phone,
		Role:        u.Role,
		CompanyName: u.CompanyName,
		INN:         u.INN,
		RegionID:    u.RegionID,
		IsActive:    u.IsActive,
		IsVerified:  u.IsVerified,
		CreatedAt:   u.CreatedAt,
		UpdatedAt:   u.UpdatedAt,
	}
}
