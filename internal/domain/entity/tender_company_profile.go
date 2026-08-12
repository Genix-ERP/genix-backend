package entity

import (
	"time"

	"github.com/google/uuid"
)

type TenderCompanyProfile struct {
	ID            uuid.UUID  `json:"id" db:"id"`
	UserID        uuid.UUID  `json:"user_id" db:"user_id"`
	TenantID      *uuid.UUID `json:"tenant_id" db:"tenant_id"`
	Role          string     `json:"role" db:"role"`
	CompanyName   string     `json:"company_name" db:"company_name"`
	INN           string     `json:"inn" db:"inn"`
	Phone         string     `json:"phone" db:"phone"`
	RegionID      *uuid.UUID `json:"region_id" db:"region_id"`
	Address       string     `json:"address" db:"address"`
	Logo          string     `json:"logo" db:"logo"`
	Banner        string     `json:"banner" db:"banner"`
	Description   string     `json:"description" db:"description"`
	Website       string     `json:"website" db:"website"`
	ActivityAreas []string   `json:"activity_areas" db:"activity_areas"`
	LicenseNumber string     `json:"license_number" db:"license_number"`
	LicenseFile   string     `json:"license_file" db:"license_file"`
	IsVerified    bool       `json:"is_verified" db:"is_verified"`
	VerifiedAt    *time.Time `json:"verified_at" db:"verified_at"`
	Rating        float64    `json:"rating" db:"rating"`
	ReviewCount   int        `json:"review_count" db:"review_count"`
	TenderCount   int        `json:"tender_count" db:"tender_count"`
	BidCount      int        `json:"bid_count" db:"bid_count"`
	WonCount      int        `json:"won_count" db:"won_count"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt     *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
}

type CreateCompanyProfileInput struct {
	Role          string     `json:"role" binding:"required,oneof=buyer supplier"`
	CompanyName   string     `json:"company_name" binding:"required"`
	INN           string     `json:"inn" binding:"required"`
	Phone         string     `json:"phone" binding:"required"`
	RegionID      *uuid.UUID `json:"region_id"`
	Address       string     `json:"address"`
	ActivityAreas []string   `json:"activity_areas"`
	LicenseNumber string     `json:"license_number"`
}

type UpdateCompanyProfileInput struct {
	CompanyName   string     `json:"company_name"`
	Phone         string     `json:"phone"`
	RegionID      *uuid.UUID `json:"region_id"`
	Address       string     `json:"address"`
	Description   string     `json:"description"`
	Website       string     `json:"website"`
	ActivityAreas []string   `json:"activity_areas"`
	LicenseNumber string     `json:"license_number"`
}

type CompanyProfileResponse struct {
	ID            uuid.UUID  `json:"id"`
	UserID        uuid.UUID  `json:"user_id"`
	Role          string     `json:"role"`
	CompanyName   string     `json:"company_name"`
	INN           string     `json:"inn"`
	Phone         string     `json:"phone"`
	RegionID      *uuid.UUID `json:"region_id"`
	RegionName    string     `json:"region_name,omitempty"`
	Address       string     `json:"address"`
	Logo          string     `json:"logo"`
	Description   string     `json:"description"`
	Website       string     `json:"website"`
	IsVerified    bool       `json:"is_verified"`
	Rating        float64    `json:"rating"`
	ReviewCount   int        `json:"review_count"`
	TenderCount   int        `json:"tender_count"`
	BidCount      int        `json:"bid_count"`
	WonCount      int        `json:"won_count"`
	ActivityAreas []string   `json:"activity_areas"`
	CreatedAt     time.Time  `json:"created_at"`
}
