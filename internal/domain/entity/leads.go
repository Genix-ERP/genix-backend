package entity

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// LeadStatus represents the status of a lead
type LeadStatus string

const (
	LeadStatusNew        LeadStatus = "new"
	LeadStatusContacted  LeadStatus = "contacted"
	LeadStatusInProgress LeadStatus = "in_progress"
	LeadStatusQualified  LeadStatus = "qualified"
	LeadStatusLost       LeadStatus = "lost"
)

// LeadSource represents the source of a lead
type LeadSource string

const (
	LeadSourceWebsite      LeadSource = "website"
	LeadSourceReferral     LeadSource = "referral"
	LeadSourceSocialMedia  LeadSource = "social_media"
	LeadSourceColdCall     LeadSource = "cold_call"
	LeadSourceAdvertisement LeadSource = "advertisement"
	LeadSourceOther        LeadSource = "other"
)

// Lead represents a sales lead
type Lead struct {
	ID              uuid.UUID    `json:"id" db:"id"`
	TenantID        uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	OrganizationID  *uuid.UUID   `json:"organization_id,omitempty" db:"organization_id"`
	ContactName     string       `json:"contact_name" db:"contact_name"`
	CompanyName     *string      `json:"company_name,omitempty" db:"company_name"`
	Email           string       `json:"email" db:"email"`
	Phone           *string      `json:"phone,omitempty" db:"phone"`
	Status          LeadStatus   `json:"status" db:"status"`
	Source          LeadSource   `json:"source" db:"source"`
	Notes           *string      `json:"notes,omitempty" db:"notes"`
	ExpectedValue   *float64     `json:"expected_value,omitempty" db:"expected_value"`
	AssignedTo      *uuid.UUID   `json:"assigned_to,omitempty" db:"assigned_to"`
	ConvertedTo     *uuid.UUID   `json:"converted_to,omitempty" db:"converted_to"` // Contact ID if converted
	ConvertedAt     *time.Time   `json:"converted_at,omitempty" db:"converted_at"`
	CreatedBy       *uuid.UUID   `json:"created_by,omitempty" db:"created_by"`
	CreatedAt       time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt       sql.NullTime `json:"-" db:"deleted_at"`

	// Populated from joins
	AssignedToName *string `json:"assigned_to_name,omitempty" db:"assigned_to_name"`
}

// CreateLeadInput represents input for creating a lead
type CreateLeadInput struct {
	ContactName   string     `json:"contact_name" binding:"required,min=1,max=255"`
	CompanyName   string     `json:"company_name,omitempty"`
	Email         string     `json:"email,omitempty" binding:"omitempty,email"`
	Phone         string     `json:"phone,omitempty"`
	Status        LeadStatus `json:"status,omitempty"`
	Source        LeadSource `json:"source,omitempty"`
	Notes         string     `json:"notes,omitempty"`
	ExpectedValue *float64   `json:"expected_value,omitempty"`
	AssignedTo    string     `json:"assigned_to,omitempty"`
}

// UpdateLeadInput represents input for updating a lead
type UpdateLeadInput struct {
	ContactName   *string     `json:"contact_name,omitempty"`
	CompanyName   *string     `json:"company_name,omitempty"`
	Email         *string     `json:"email,omitempty"`
	Phone         *string     `json:"phone,omitempty"`
	Status        *LeadStatus `json:"status,omitempty"`
	Source        *LeadSource `json:"source,omitempty"`
	Notes         *string     `json:"notes,omitempty"`
	ExpectedValue *float64    `json:"expected_value,omitempty"`
	AssignedTo    *string     `json:"assigned_to,omitempty"`
}

// LeadListFilter represents filters for listing leads
type LeadListFilter struct {
	Search     string     `form:"search"`
	Status     LeadStatus `form:"status"`
	Source     LeadSource `form:"source"`
	AssignedTo string     `form:"assigned_to"`
	DateFrom   *time.Time `form:"date_from"`
	DateTo     *time.Time `form:"date_to"`
}

// LeadResponse represents the API response for a lead
type LeadResponse struct {
	ID             uuid.UUID  `json:"id"`
	ContactName    string     `json:"contact_name"`
	CompanyName    *string    `json:"company_name,omitempty"`
	Email          string     `json:"email"`
	Phone          *string    `json:"phone,omitempty"`
	Status         LeadStatus `json:"status"`
	Source         LeadSource `json:"source"`
	Notes          *string    `json:"notes,omitempty"`
	ExpectedValue  *float64   `json:"expected_value,omitempty"`
	AssignedTo     *uuid.UUID `json:"assigned_to,omitempty"`
	AssignedToName *string    `json:"assigned_to_name,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// LeadStats represents aggregated lead statistics
type LeadStats struct {
	TotalLeads      int     `json:"total_leads"`
	NewLeads        int     `json:"new_leads"`
	ContactedLeads  int     `json:"contacted_leads"`
	InProgressLeads int     `json:"in_progress_leads"`
	QualifiedLeads  int     `json:"qualified_leads"`
	LostLeads       int     `json:"lost_leads"`
	ConversionRate  float64 `json:"conversion_rate"`
	TotalValue      float64 `json:"total_value"`
}
