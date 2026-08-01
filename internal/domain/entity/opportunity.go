package entity

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// OpportunityStage represents the stage of an opportunity
type OpportunityStage string

const (
	OpportunityStageQualification OpportunityStage = "qualification"
	OpportunityStageNeedsAnalysis OpportunityStage = "needs_analysis"
	OpportunityStageProposal      OpportunityStage = "proposal"
	OpportunityStageNegotiation   OpportunityStage = "negotiation"
	OpportunityStageClosedWon     OpportunityStage = "closed_won"
	OpportunityStageClosedLost    OpportunityStage = "closed_lost"
)

// OpportunityPriority represents the priority of an opportunity
type OpportunityPriority string

const (
	OpportunityPriorityLow    OpportunityPriority = "low"
	OpportunityPriorityMedium OpportunityPriority = "medium"
	OpportunityPriorityHigh   OpportunityPriority = "high"
	OpportunityPriorityUrgent OpportunityPriority = "urgent"
)

// PipelineStage represents a customizable pipeline stage
type PipelineStage struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	TenantID       uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	Name           string     `json:"name" db:"name"`
	CustomName     *string    `json:"custom_name" db:"custom_name"`
	Code           string     `json:"code" db:"code"`
	Sequence       int        `json:"sequence" db:"sequence"`
	Probability    float64    `json:"probability" db:"probability"`
	IsWon          bool       `json:"is_won" db:"is_won"`
	IsLost         bool       `json:"is_lost" db:"is_lost"`
	Color          string     `json:"color" db:"color"`
	IsActive       bool       `json:"is_active" db:"is_active"`
	PipelineType   string     `json:"pipeline_type" db:"pipeline_type"`
	OrganizationID *uuid.UUID `json:"organization_id" db:"organization_id"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at" db:"updated_at"`
}

// Opportunity represents a sales opportunity/deal
type Opportunity struct {
	ID                uuid.UUID         `json:"id" db:"id"`
	TenantID          uuid.UUID         `json:"tenant_id" db:"tenant_id"`
	Name              string            `json:"name" db:"name"`
	Code              *string           `json:"code,omitempty" db:"code"`
	ContactID         *uuid.UUID        `json:"contact_id,omitempty" db:"contact_id"`
	LeadID            *uuid.UUID        `json:"lead_id,omitempty" db:"lead_id"`
	StageID           *uuid.UUID        `json:"stage_id,omitempty" db:"stage_id"`
	Stage             OpportunityStage  `json:"stage" db:"stage"`
	Probability       float64           `json:"probability" db:"probability"`
	ExpectedRevenue   float64           `json:"expected_revenue" db:"expected_revenue"`
	ActualRevenue     *float64          `json:"actual_revenue,omitempty" db:"actual_revenue"`
	Currency          string            `json:"currency" db:"currency"`
	ExpectedCloseDate *time.Time        `json:"expected_close_date,omitempty" db:"expected_close_date"`
	ActualCloseDate   *time.Time        `json:"actual_close_date,omitempty" db:"actual_close_date"`
	Source            *string           `json:"source,omitempty" db:"source"`
	Priority          OpportunityPriority `json:"priority" db:"priority"`
	AssignedTo        *uuid.UUID        `json:"assigned_to,omitempty" db:"assigned_to"`
	TeamID            *uuid.UUID        `json:"team_id,omitempty" db:"team_id"`
	Description       *string           `json:"description,omitempty" db:"description"`
	NextStep          *string           `json:"next_step,omitempty" db:"next_step"`
	NextStepDate      *time.Time        `json:"next_step_date,omitempty" db:"next_step_date"`
	LostReason        *string           `json:"lost_reason,omitempty" db:"lost_reason"`
	Competitor        *string           `json:"competitor,omitempty" db:"competitor"`
	Tags              json.RawMessage   `json:"tags" db:"tags"`
	CustomFields      json.RawMessage   `json:"custom_fields" db:"custom_fields"`
	CreatedBy         *uuid.UUID        `json:"created_by,omitempty" db:"created_by"`
	CreatedAt         time.Time         `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at" db:"updated_at"`
	DeletedAt         sql.NullTime      `json:"-" db:"deleted_at"`

	// Populated from joins
	ContactName    *string `json:"contact_name,omitempty" db:"contact_name"`
	AssignedToName *string `json:"assigned_to_name,omitempty" db:"assigned_to_name"`
	StageName      *string `json:"stage_name,omitempty" db:"stage_name"`
}

// CreateOpportunityInput represents input for creating an opportunity
type CreateOpportunityInput struct {
	Name              string  `json:"name" binding:"required,min=1,max=255"`
	Code              string  `json:"code,omitempty"`
	ContactID         string  `json:"contact_id,omitempty"`
	LeadID            string  `json:"lead_id,omitempty"`
	StageID           string  `json:"stage_id,omitempty"`
	Stage             string  `json:"stage,omitempty"`
	Probability       float64 `json:"probability,omitempty"`
	ExpectedRevenue   float64 `json:"expected_revenue,omitempty"`
	Currency          string  `json:"currency,omitempty"`
	ExpectedCloseDate string  `json:"expected_close_date,omitempty"`
	Source            string  `json:"source,omitempty"`
	Priority          string  `json:"priority,omitempty"`
	AssignedTo        string  `json:"assigned_to,omitempty"`
	Description       string  `json:"description,omitempty"`
	NextStep          string  `json:"next_step,omitempty"`
	NextStepDate      string  `json:"next_step_date,omitempty"`
	Tags              []string `json:"tags,omitempty"`
}

// UpdateOpportunityInput represents input for updating an opportunity
type UpdateOpportunityInput struct {
	Name              *string  `json:"name,omitempty"`
	ContactID         *string  `json:"contact_id,omitempty"`
	StageID           *string  `json:"stage_id,omitempty"`
	Stage             *string  `json:"stage,omitempty"`
	Probability       *float64 `json:"probability,omitempty"`
	ExpectedRevenue   *float64 `json:"expected_revenue,omitempty"`
	ActualRevenue     *float64 `json:"actual_revenue,omitempty"`
	Currency          *string  `json:"currency,omitempty"`
	ExpectedCloseDate *string  `json:"expected_close_date,omitempty"`
	ActualCloseDate   *string  `json:"actual_close_date,omitempty"`
	Source            *string  `json:"source,omitempty"`
	Priority          *string  `json:"priority,omitempty"`
	AssignedTo        *string  `json:"assigned_to,omitempty"`
	Description       *string  `json:"description,omitempty"`
	NextStep          *string  `json:"next_step,omitempty"`
	NextStepDate      *string  `json:"next_step_date,omitempty"`
	LostReason        *string  `json:"lost_reason,omitempty"`
	Competitor        *string  `json:"competitor,omitempty"`
	Tags              []string `json:"tags,omitempty"`
}

// OpportunityListFilter represents filters for listing opportunities
type OpportunityListFilter struct {
	Search        string `form:"search"`
	Stage         string `form:"stage"`
	StageID       string `form:"stage_id"`
	ContactID     string `form:"contact_id"`
	AssignedTo    string `form:"assigned_to"`
	Priority      string `form:"priority"`
	Source        string `form:"source"`
	DateFrom      string `form:"date_from"`
	DateTo        string `form:"date_to"`
	MinRevenue    *float64 `form:"min_revenue"`
	MaxRevenue    *float64 `form:"max_revenue"`
	IncludeClosed bool   `form:"include_closed"`
}

// OpportunityResponse represents the API response for an opportunity
type OpportunityResponse struct {
	ID                uuid.UUID           `json:"id"`
	Name              string              `json:"name"`
	Code              *string             `json:"code,omitempty"`
	ContactID         *uuid.UUID          `json:"contact_id,omitempty"`
	ContactName       *string             `json:"contact_name,omitempty"`
	LeadID            *uuid.UUID          `json:"lead_id,omitempty"`
	StageID           *uuid.UUID          `json:"stage_id,omitempty"`
	StageName         *string             `json:"stage_name,omitempty"`
	Stage             OpportunityStage    `json:"stage"`
	Probability       float64             `json:"probability"`
	ExpectedRevenue   float64             `json:"expected_revenue"`
	ActualRevenue     *float64            `json:"actual_revenue,omitempty"`
	Currency          string              `json:"currency"`
	ExpectedCloseDate *time.Time          `json:"expected_close_date,omitempty"`
	ActualCloseDate   *time.Time          `json:"actual_close_date,omitempty"`
	Source            *string             `json:"source,omitempty"`
	Priority          OpportunityPriority `json:"priority"`
	AssignedTo        *uuid.UUID          `json:"assigned_to,omitempty"`
	AssignedToName    *string             `json:"assigned_to_name,omitempty"`
	Description       *string             `json:"description,omitempty"`
	NextStep          *string             `json:"next_step,omitempty"`
	NextStepDate      *time.Time          `json:"next_step_date,omitempty"`
	LostReason        *string             `json:"lost_reason,omitempty"`
	Tags              []string            `json:"tags"`
	CreatedAt         time.Time           `json:"created_at"`
	UpdatedAt         time.Time           `json:"updated_at"`
}

// OpportunityStats represents aggregated opportunity statistics
type OpportunityStats struct {
	TotalOpportunities int     `json:"total_opportunities"`
	TotalValue         float64 `json:"total_value"`
	WeightedValue      float64 `json:"weighted_value"`
	WonCount           int     `json:"won_count"`
	WonValue           float64 `json:"won_value"`
	LostCount          int     `json:"lost_count"`
	LostValue          float64 `json:"lost_value"`
	OpenCount          int     `json:"open_count"`
	OpenValue          float64 `json:"open_value"`
	WinRate            float64 `json:"win_rate"`
	AverageDealSize    float64 `json:"average_deal_size"`
	AverageSalesCycle  int     `json:"average_sales_cycle_days"`
}

// OpportunityByStage represents opportunities grouped by stage
type OpportunityByStage struct {
	Stage       string  `json:"stage"`
	StageName   string  `json:"stage_name"`
	Count       int     `json:"count"`
	TotalValue  float64 `json:"total_value"`
	Probability float64 `json:"probability"`
}

// CreatePipelineStageInput represents input for creating a pipeline stage
type CreatePipelineStageInput struct {
	Name           string  `json:"name" binding:"required,min=1,max=100"`
	Code           string  `json:"code" binding:"required,min=1,max=50"`
	Sequence       int     `json:"sequence"`
	Probability    float64 `json:"probability"`
	IsWon          bool    `json:"is_won"`
	IsLost         bool    `json:"is_lost"`
	Color          string  `json:"color"`
	PipelineType   string  `json:"pipeline_type"`
	PipelineID     string  `json:"pipeline_id,omitempty"` // CRM v2: which funnel the stage belongs to
	OrganizationID string  `json:"organization_id"`
}

// UpdatePipelineStageInput represents input for updating a pipeline stage
type UpdatePipelineStageInput struct {
	Name        *string  `json:"name,omitempty"`
	CustomName  *string  `json:"custom_name"`
	Sequence    *int     `json:"sequence,omitempty"`
	Probability *float64 `json:"probability,omitempty"`
	IsWon       *bool    `json:"is_won,omitempty"`
	IsLost      *bool    `json:"is_lost,omitempty"`
	Color       *string  `json:"color,omitempty"`
	IsActive    *bool    `json:"is_active,omitempty"`
}
