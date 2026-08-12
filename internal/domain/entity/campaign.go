package entity

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// CampaignType represents the type of campaign
type CampaignType string

const (
	CampaignTypeEmail    CampaignType = "email"
	CampaignTypeSocial   CampaignType = "social"
	CampaignTypeAds      CampaignType = "ads"
	CampaignTypeEvent    CampaignType = "event"
	CampaignTypeReferral CampaignType = "referral"
	CampaignTypeContent  CampaignType = "content"
	CampaignTypeOther    CampaignType = "other"
)

// CampaignStatus represents the status of a campaign
type CampaignStatus string

const (
	CampaignStatusDraft     CampaignStatus = "draft"
	CampaignStatusScheduled CampaignStatus = "scheduled"
	CampaignStatusActive    CampaignStatus = "active"
	CampaignStatusPaused    CampaignStatus = "paused"
	CampaignStatusCompleted CampaignStatus = "completed"
	CampaignStatusCancelled CampaignStatus = "cancelled"
)

// CampaignMemberStatus represents the status of a campaign member
type CampaignMemberStatus string

const (
	CampaignMemberStatusSent         CampaignMemberStatus = "sent"
	CampaignMemberStatusOpened       CampaignMemberStatus = "opened"
	CampaignMemberStatusClicked      CampaignMemberStatus = "clicked"
	CampaignMemberStatusResponded    CampaignMemberStatus = "responded"
	CampaignMemberStatusConverted    CampaignMemberStatus = "converted"
	CampaignMemberStatusUnsubscribed CampaignMemberStatus = "unsubscribed"
)

// Campaign represents a marketing campaign
type Campaign struct {
	ID                uuid.UUID       `json:"id" db:"id"`
	TenantID          uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	Name              string          `json:"name" db:"name"`
	Code              string          `json:"code" db:"code"`
	CampaignType      CampaignType    `json:"campaign_type" db:"campaign_type"`
	Status            CampaignStatus  `json:"status" db:"status"`
	StartDate         *time.Time      `json:"start_date,omitempty" db:"start_date"`
	EndDate           *time.Time      `json:"end_date,omitempty" db:"end_date"`
	BudgetedCost      float64         `json:"budgeted_cost" db:"budgeted_cost"`
	ActualCost        float64         `json:"actual_cost" db:"actual_cost"`
	Currency          string          `json:"currency" db:"currency"`
	TargetAudience    *string         `json:"target_audience,omitempty" db:"target_audience"`
	TargetLeads       int             `json:"target_leads" db:"target_leads"`
	TargetConversions int             `json:"target_conversions" db:"target_conversions"`
	ActualLeads       int             `json:"actual_leads" db:"actual_leads"`
	ActualConversions int             `json:"actual_conversions" db:"actual_conversions"`
	TotalRevenue      float64         `json:"total_revenue" db:"total_revenue"`
	Description       *string         `json:"description,omitempty" db:"description"`
	MessageTemplate   *string         `json:"message_template,omitempty" db:"message_template"`
	OwnerID           *uuid.UUID      `json:"owner_id,omitempty" db:"owner_id"`
	TeamMembers       json.RawMessage `json:"team_members" db:"team_members" swaggertype:"object"`
	Tags              json.RawMessage `json:"tags" db:"tags" swaggertype:"object"`
	CustomFields      json.RawMessage `json:"custom_fields" db:"custom_fields" swaggertype:"object"`
	CreatedBy         *uuid.UUID      `json:"created_by,omitempty" db:"created_by"`
	CreatedAt         time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at" db:"updated_at"`
	DeletedAt         sql.NullTime    `json:"-" db:"deleted_at"`

	// Populated from joins
	OwnerName *string `json:"owner_name,omitempty" db:"owner_name"`
}

// CampaignMember represents a lead or contact in a campaign
type CampaignMember struct {
	ID          uuid.UUID            `json:"id" db:"id"`
	CampaignID  uuid.UUID            `json:"campaign_id" db:"campaign_id"`
	LeadID      *uuid.UUID           `json:"lead_id,omitempty" db:"lead_id"`
	ContactID   *uuid.UUID           `json:"contact_id,omitempty" db:"contact_id"`
	Status      CampaignMemberStatus `json:"status" db:"status"`
	SentAt      *time.Time           `json:"sent_at,omitempty" db:"sent_at"`
	OpenedAt    *time.Time           `json:"opened_at,omitempty" db:"opened_at"`
	ClickedAt   *time.Time           `json:"clicked_at,omitempty" db:"clicked_at"`
	RespondedAt *time.Time           `json:"responded_at,omitempty" db:"responded_at"`
	ConvertedAt *time.Time           `json:"converted_at,omitempty" db:"converted_at"`
	Notes       *string              `json:"notes,omitempty" db:"notes"`
	CreatedAt   time.Time            `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at" db:"updated_at"`

	// Populated from joins
	LeadName    *string `json:"lead_name,omitempty" db:"lead_name"`
	ContactName *string `json:"contact_name,omitempty" db:"contact_name"`
	Email       *string `json:"email,omitempty" db:"email"`
}

// CreateCampaignInput represents input for creating a campaign
type CreateCampaignInput struct {
	Name              string   `json:"name" binding:"required,min=1,max=255"`
	Code              string   `json:"code" binding:"required,min=1,max=50"`
	CampaignType      string   `json:"campaign_type" binding:"required,oneof=email social ads event referral content other"`
	Status            string   `json:"status,omitempty"`
	StartDate         string   `json:"start_date,omitempty"`
	EndDate           string   `json:"end_date,omitempty"`
	BudgetedCost      float64  `json:"budgeted_cost,omitempty"`
	Currency          string   `json:"currency,omitempty"`
	TargetAudience    string   `json:"target_audience,omitempty"`
	TargetLeads       int      `json:"target_leads,omitempty"`
	TargetConversions int      `json:"target_conversions,omitempty"`
	Description       string   `json:"description,omitempty"`
	MessageTemplate   string   `json:"message_template,omitempty"`
	OwnerID           string   `json:"owner_id,omitempty"`
	TeamMembers       []string `json:"team_members,omitempty"`
	Tags              []string `json:"tags,omitempty"`
}

// UpdateCampaignInput represents input for updating a campaign
type UpdateCampaignInput struct {
	Name              *string  `json:"name,omitempty"`
	CampaignType      *string  `json:"campaign_type,omitempty"`
	Status            *string  `json:"status,omitempty"`
	StartDate         *string  `json:"start_date,omitempty"`
	EndDate           *string  `json:"end_date,omitempty"`
	BudgetedCost      *float64 `json:"budgeted_cost,omitempty"`
	ActualCost        *float64 `json:"actual_cost,omitempty"`
	Currency          *string  `json:"currency,omitempty"`
	TargetAudience    *string  `json:"target_audience,omitempty"`
	TargetLeads       *int     `json:"target_leads,omitempty"`
	TargetConversions *int     `json:"target_conversions,omitempty"`
	Description       *string  `json:"description,omitempty"`
	MessageTemplate   *string  `json:"message_template,omitempty"`
	OwnerID           *string  `json:"owner_id,omitempty"`
	TeamMembers       []string `json:"team_members,omitempty"`
	Tags              []string `json:"tags,omitempty"`
}

// CampaignListFilter represents filters for listing campaigns
type CampaignListFilter struct {
	Search       string `form:"search"`
	CampaignType string `form:"campaign_type"`
	Status       string `form:"status"`
	OwnerID      string `form:"owner_id"`
	DateFrom     string `form:"date_from"`
	DateTo       string `form:"date_to"`
}

// CampaignResponse represents the API response for a campaign
type CampaignResponse struct {
	ID                uuid.UUID      `json:"id"`
	Name              string         `json:"name"`
	Code              string         `json:"code"`
	CampaignType      CampaignType   `json:"campaign_type"`
	Status            CampaignStatus `json:"status"`
	StartDate         *time.Time     `json:"start_date,omitempty"`
	EndDate           *time.Time     `json:"end_date,omitempty"`
	BudgetedCost      float64        `json:"budgeted_cost"`
	ActualCost        float64        `json:"actual_cost"`
	Currency          string         `json:"currency"`
	TargetAudience    *string        `json:"target_audience,omitempty"`
	TargetLeads       int            `json:"target_leads"`
	TargetConversions int            `json:"target_conversions"`
	ActualLeads       int            `json:"actual_leads"`
	ActualConversions int            `json:"actual_conversions"`
	TotalRevenue      float64        `json:"total_revenue"`
	Description       *string        `json:"description,omitempty"`
	OwnerID           *uuid.UUID     `json:"owner_id,omitempty"`
	OwnerName         *string        `json:"owner_name,omitempty"`
	Tags              []string       `json:"tags"`
	ROI               float64        `json:"roi"`
	ConversionRate    float64        `json:"conversion_rate"`
	MemberCount       int            `json:"member_count"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

// CampaignStats represents campaign statistics
type CampaignStats struct {
	TotalCampaigns    int            `json:"total_campaigns"`
	ActiveCampaigns   int            `json:"active_campaigns"`
	TotalBudget       float64        `json:"total_budget"`
	TotalSpent        float64        `json:"total_spent"`
	TotalLeads        int            `json:"total_leads"`
	TotalConversions  int            `json:"total_conversions"`
	TotalRevenue      float64        `json:"total_revenue"`
	AverageROI        float64        `json:"average_roi"`
	AverageConversion float64        `json:"average_conversion_rate"`
	ByType            map[string]int `json:"by_type"`
	ByStatus          map[string]int `json:"by_status"`
}

// AddCampaignMemberInput represents input for adding members to a campaign
type AddCampaignMemberInput struct {
	LeadIDs    []string `json:"lead_ids,omitempty"`
	ContactIDs []string `json:"contact_ids,omitempty"`
}

// UpdateCampaignMemberInput represents input for updating a campaign member
type UpdateCampaignMemberInput struct {
	Status *string `json:"status,omitempty"`
	Notes  *string `json:"notes,omitempty"`
}

// CampaignMemberResponse represents the API response for a campaign member
type CampaignMemberResponse struct {
	ID          uuid.UUID            `json:"id"`
	CampaignID  uuid.UUID            `json:"campaign_id"`
	LeadID      *uuid.UUID           `json:"lead_id,omitempty"`
	LeadName    *string              `json:"lead_name,omitempty"`
	ContactID   *uuid.UUID           `json:"contact_id,omitempty"`
	ContactName *string              `json:"contact_name,omitempty"`
	Email       *string              `json:"email,omitempty"`
	Status      CampaignMemberStatus `json:"status"`
	SentAt      *time.Time           `json:"sent_at,omitempty"`
	OpenedAt    *time.Time           `json:"opened_at,omitempty"`
	ClickedAt   *time.Time           `json:"clicked_at,omitempty"`
	RespondedAt *time.Time           `json:"responded_at,omitempty"`
	ConvertedAt *time.Time           `json:"converted_at,omitempty"`
	Notes       *string              `json:"notes,omitempty"`
	CreatedAt   time.Time            `json:"created_at"`
}
