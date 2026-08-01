package entity

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// LeadStatus represents the status of a lead. Since CRM v2 (migration 446)
// the source of truth is stage_id → pipeline_stages; status mirrors the
// stage code for legacy readers and workflow-rule conditions.
type LeadStatus string

const (
	LeadStatusNew        LeadStatus = "new"
	LeadStatusContacted  LeadStatus = "contacted"
	LeadStatusInProgress LeadStatus = "in_progress"
	LeadStatusQualified  LeadStatus = "qualified"
	LeadStatusWon        LeadStatus = "won"
	LeadStatusLost       LeadStatus = "lost"
)

// LeadSource represents the source of a lead (free text since 446; these are
// the seeded defaults)
type LeadSource string

const (
	LeadSourceWebsite       LeadSource = "website"
	LeadSourceTelegram      LeadSource = "telegram"
	LeadSourceReferral      LeadSource = "referral"
	LeadSourceSocialMedia   LeadSource = "social_media"
	LeadSourceColdCall      LeadSource = "cold_call"
	LeadSourceAdvertisement LeadSource = "advertisement"
	LeadSourceOther         LeadSource = "other"
)

// Lead represents a sales lead — since CRM v2 the lead IS the deal (bitim):
// it carries the amount, the pipeline stage and, once won, the partner link.
type Lead struct {
	ID                    uuid.UUID    `json:"id" db:"id"`
	TenantID              uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	OrganizationID        *uuid.UUID   `json:"organization_id,omitempty" db:"organization_id"`
	ContactName           string       `json:"contact_name" db:"contact_name"`
	CompanyName           *string      `json:"company_name,omitempty" db:"company_name"`
	Email                 string       `json:"email" db:"email"`
	Phone                 *string      `json:"phone,omitempty" db:"phone"`
	Status                LeadStatus   `json:"status" db:"status"`
	Source                LeadSource   `json:"source" db:"source"`
	Notes                 *string      `json:"notes,omitempty" db:"notes"`
	ExpectedValue         *float64     `json:"expected_value,omitempty" db:"expected_value"`
	Currency              string       `json:"currency" db:"currency"`
	PipelineID            *uuid.UUID   `json:"pipeline_id,omitempty" db:"pipeline_id"`
	StageID               *uuid.UUID   `json:"stage_id,omitempty" db:"stage_id"`
	ResponsibleEmployeeID *uuid.UUID   `json:"responsible_employee_id,omitempty" db:"responsible_employee_id"`
	PartnerID             *uuid.UUID   `json:"partner_id,omitempty" db:"partner_id"`
	LostReasonID          *uuid.UUID   `json:"lost_reason_id,omitempty" db:"lost_reason_id"`
	LostNote              *string      `json:"lost_note,omitempty" db:"lost_note"`
	WonAt                 *time.Time   `json:"won_at,omitempty" db:"won_at"`
	LostAt                *time.Time   `json:"lost_at,omitempty" db:"lost_at"`
	LastActivityAt        *time.Time   `json:"last_activity_at,omitempty" db:"last_activity_at"`
	AssignedTo            *uuid.UUID   `json:"assigned_to,omitempty" db:"assigned_to"`
	ConvertedTo           *uuid.UUID   `json:"converted_to,omitempty" db:"converted_to"` // Contact ID if converted
	ConvertedAt           *time.Time   `json:"converted_at,omitempty" db:"converted_at"`
	CreatedBy             *uuid.UUID   `json:"created_by,omitempty" db:"created_by"`
	CreatedAt             time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt             time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt             sql.NullTime `json:"-" db:"deleted_at"`

	// Populated from joins
	AssignedToName *string `json:"assigned_to_name,omitempty" db:"assigned_to_name"`
}

// CreateLeadInput represents input for creating a lead
type CreateLeadInput struct {
	ContactName           string     `json:"contact_name" binding:"required,min=1,max=255"`
	CompanyName           string     `json:"company_name,omitempty"`
	Email                 string     `json:"email,omitempty" binding:"omitempty,email"`
	Phone                 string     `json:"phone,omitempty"`
	Status                LeadStatus `json:"status,omitempty"`
	Source                LeadSource `json:"source,omitempty"`
	Notes                 string     `json:"notes,omitempty"`
	ExpectedValue         *float64   `json:"expected_value,omitempty"`
	Currency              string     `json:"currency,omitempty"`
	PipelineID            string     `json:"pipeline_id,omitempty"`
	StageID               string     `json:"stage_id,omitempty"`
	ResponsibleEmployeeID string     `json:"responsible_employee_id,omitempty"`
	AssignedTo            string     `json:"assigned_to,omitempty"`
}

// UpdateLeadInput represents input for updating a lead. Stage moves and
// won/lost transitions go through the dedicated endpoints, not this input.
type UpdateLeadInput struct {
	ContactName           *string     `json:"contact_name,omitempty"`
	CompanyName           *string     `json:"company_name,omitempty"`
	Email                 *string     `json:"email,omitempty"`
	Phone                 *string     `json:"phone,omitempty"`
	Status                *LeadStatus `json:"status,omitempty"`
	Source                *LeadSource `json:"source,omitempty"`
	Notes                 *string     `json:"notes,omitempty"`
	ExpectedValue         *float64    `json:"expected_value,omitempty"`
	Currency              *string     `json:"currency,omitempty"`
	ResponsibleEmployeeID *string     `json:"responsible_employee_id,omitempty"`
	AssignedTo            *string     `json:"assigned_to,omitempty"`
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
	ID                    uuid.UUID  `json:"id"`
	ContactName           string     `json:"contact_name"`
	CompanyName           *string    `json:"company_name,omitempty"`
	Email                 string     `json:"email"`
	Phone                 *string    `json:"phone,omitempty"`
	Status                LeadStatus `json:"status"`
	Source                LeadSource `json:"source"`
	Notes                 *string    `json:"notes,omitempty"`
	ExpectedValue         *float64   `json:"expected_value,omitempty"`
	Currency              string     `json:"currency,omitempty"`
	PipelineID            *uuid.UUID `json:"pipeline_id,omitempty"`
	StageID               *uuid.UUID `json:"stage_id,omitempty"`
	StageCode             *string    `json:"stage_code,omitempty"`
	StageName             *string    `json:"stage_name,omitempty"`
	ResponsibleEmployeeID *uuid.UUID `json:"responsible_employee_id,omitempty"`
	ResponsibleName       *string    `json:"responsible_name,omitempty"`
	PartnerID             *uuid.UUID `json:"partner_id,omitempty"`
	PartnerName           *string    `json:"partner_name,omitempty"`
	LostReasonID          *uuid.UUID `json:"lost_reason_id,omitempty"`
	LostReasonName        *string    `json:"lost_reason_name,omitempty"`
	LostNote              *string    `json:"lost_note,omitempty"`
	WonAt                 *time.Time `json:"won_at,omitempty"`
	LostAt                *time.Time `json:"lost_at,omitempty"`
	LastActivityAt        *time.Time `json:"last_activity_at,omitempty"`
	OpenTaskCount         int        `json:"open_task_count"`
	AssignedTo            *uuid.UUID `json:"assigned_to,omitempty"`
	AssignedToName        *string    `json:"assigned_to_name,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
	LastModifiedBy        *string    `json:"last_modified_by,omitempty"`
	LastModifiedAt        *time.Time `json:"last_modified_at,omitempty"`
}

// LeadStats represents aggregated lead statistics for the CRM header row
type LeadStats struct {
	TotalLeads      int     `json:"total_leads"`
	OpenLeads       int     `json:"open_leads"`
	OpenValue       float64 `json:"open_value"`
	WonLeads        int     `json:"won_leads"`
	LostLeads       int     `json:"lost_leads"`
	WonThisMonth    int     `json:"won_this_month"`
	WonValueMonth   float64 `json:"won_value_month"`
	ConversionRate  float64 `json:"conversion_rate"` // won / (won+lost), all time
	ConversionMonth float64 `json:"conversion_month"`
	AvgDealSize     float64 `json:"avg_deal_size"` // avg amount of won leads
	TotalValue      float64 `json:"total_value"`

	// Legacy per-status counts kept for old consumers
	NewLeads        int `json:"new_leads"`
	ContactedLeads  int `json:"contacted_leads"`
	InProgressLeads int `json:"in_progress_leads"`
	QualifiedLeads  int `json:"qualified_leads"`
}

// Pipeline is a sales funnel; an org can have several (e.g. Yangi qurilish /
// Ta'mirlash). Stages attach via pipeline_stages.pipeline_id.
type Pipeline struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	TenantID       uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	OrganizationID *uuid.UUID `json:"organization_id,omitempty" db:"organization_id"`
	Name           string     `json:"name" db:"name"`
	IsDefault      bool       `json:"is_default" db:"is_default"`
	IsActive       bool       `json:"is_active" db:"is_active"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at" db:"updated_at"`
}

// LostReason is a tenant-configurable loss reason (required when a lead is lost)
type LostReason struct {
	ID       uuid.UUID `json:"id" db:"id"`
	TenantID uuid.UUID `json:"tenant_id" db:"tenant_id"`
	Name     string    `json:"name" db:"name"`
	Position int       `json:"position" db:"position"`
	IsActive bool      `json:"is_active" db:"is_active"`
}
