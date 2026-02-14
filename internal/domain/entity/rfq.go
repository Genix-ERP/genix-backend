package entity

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// RFQStatus represents the status of an RFQ
type RFQStatus string

const (
	RFQStatusDraft     RFQStatus = "draft"
	RFQStatusOpen      RFQStatus = "open"
	RFQStatusClosed    RFQStatus = "closed"
	RFQStatusCancelled RFQStatus = "cancelled"
)

// RFQ represents a Request for Quotation
type RFQ struct {
	ID           uuid.UUID       `json:"id" db:"id"`
	TenantID     uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	RFQNumber    string          `json:"rfq_number" db:"rfq_number"`
	Title        string          `json:"title" db:"title"`
	Description  *string         `json:"description,omitempty" db:"description"`
	Status       RFQStatus       `json:"status" db:"status"`
	Deadline     *time.Time      `json:"deadline,omitempty" db:"deadline"`
	Terms        *string         `json:"terms,omitempty" db:"terms"`
	Notes        *string         `json:"notes,omitempty" db:"notes"`
	WinnerID     *uuid.UUID      `json:"winner_id,omitempty" db:"winner_id"`
	CustomFields json.RawMessage `json:"custom_fields" db:"custom_fields"`
	CreatedBy    *uuid.UUID      `json:"created_by,omitempty" db:"created_by"`
	CreatedAt    time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at" db:"updated_at"`
	DeletedAt    sql.NullTime    `json:"-" db:"deleted_at"`

	// Relationships
	Items       []RFQItem     `json:"items,omitempty"`
	Invitations []RFQInvite   `json:"invitations,omitempty"`
	Responses   []RFQResponse `json:"responses,omitempty"`
}

// RFQItem represents an item in an RFQ
type RFQItem struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	RFQID       uuid.UUID  `json:"rfq_id" db:"rfq_id"`
	ProductID   *uuid.UUID `json:"product_id,omitempty" db:"product_id"`
	Description string     `json:"description" db:"description"`
	Quantity    float64    `json:"quantity" db:"quantity"`
	UnitID      *uuid.UUID `json:"unit_id,omitempty" db:"unit_id"`
	Specs       *string    `json:"specs,omitempty" db:"specs"`
	Notes       *string    `json:"notes,omitempty" db:"notes"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at" db:"updated_at"`

	// Computed
	ProductName string `json:"product_name,omitempty"`
	UnitName    string `json:"unit_name,omitempty"`
}

// RFQInvite represents a vendor invitation to an RFQ
type RFQInvite struct {
	ID         uuid.UUID  `json:"id" db:"id"`
	RFQID      uuid.UUID  `json:"rfq_id" db:"rfq_id"`
	VendorID   uuid.UUID  `json:"vendor_id" db:"vendor_id"`
	InvitedAt  time.Time  `json:"invited_at" db:"invited_at"`
	ViewedAt   *time.Time `json:"viewed_at,omitempty" db:"viewed_at"`
	RespondedAt *time.Time `json:"responded_at,omitempty" db:"responded_at"`

	// Computed
	VendorName string `json:"vendor_name,omitempty"`
}

// RFQResponse represents a vendor's response to an RFQ
type RFQResponse struct {
	ID           uuid.UUID `json:"id" db:"id"`
	RFQID        uuid.UUID `json:"rfq_id" db:"rfq_id"`
	VendorID     uuid.UUID `json:"vendor_id" db:"vendor_id"`
	TotalAmount  float64   `json:"total_amount" db:"total_amount"`
	LeadTimeDays int       `json:"lead_time_days" db:"lead_time_days"`
	ValidUntil   *time.Time `json:"valid_until,omitempty" db:"valid_until"`
	Notes        *string   `json:"notes,omitempty" db:"notes"`
	IsWinner     bool      `json:"is_winner" db:"is_winner"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time `json:"updated_at" db:"updated_at"`

	// Computed
	VendorName string             `json:"vendor_name,omitempty"`
	Items      []RFQResponseItem  `json:"items,omitempty"`
}

// RFQResponseItem represents a line item in an RFQ response
type RFQResponseItem struct {
	ID         uuid.UUID `json:"id" db:"id"`
	ResponseID uuid.UUID `json:"response_id" db:"response_id"`
	ItemID     uuid.UUID `json:"item_id" db:"item_id"`
	UnitPrice  float64   `json:"unit_price" db:"unit_price"`
	TotalPrice float64   `json:"total_price" db:"total_price"`
	Notes      *string   `json:"notes,omitempty" db:"notes"`
}

// CreateRFQInput represents input for creating an RFQ
type CreateRFQInput struct {
	Title       string              `json:"title" binding:"required"`
	Description string              `json:"description,omitempty"`
	Deadline    string              `json:"deadline,omitempty"`
	Terms       string              `json:"terms,omitempty"`
	Notes       string              `json:"notes,omitempty"`
	Items       []CreateRFQItemInput `json:"items" binding:"required,min=1"`
	VendorIDs   []string            `json:"vendor_ids,omitempty"`
}

// CreateRFQItemInput represents input for an RFQ item
type CreateRFQItemInput struct {
	ProductID   string  `json:"product_id,omitempty"`
	Description string  `json:"description" binding:"required"`
	Quantity    float64 `json:"quantity" binding:"required,gt=0"`
	UnitID      string  `json:"unit_id,omitempty"`
	Specs       string  `json:"specs,omitempty"`
	Notes       string  `json:"notes,omitempty"`
}

// SubmitRFQResponseInput represents input for submitting an RFQ response
type SubmitRFQResponseInput struct {
	VendorID     string                      `json:"vendor_id" binding:"required"`
	LeadTimeDays int                         `json:"lead_time_days"`
	ValidUntil   string                      `json:"valid_until,omitempty"`
	Notes        string                      `json:"notes,omitempty"`
	Items        []SubmitRFQResponseItemInput `json:"items" binding:"required,min=1"`
}

// SubmitRFQResponseItemInput represents input for a response item
type SubmitRFQResponseItemInput struct {
	ItemID    string  `json:"item_id" binding:"required"`
	UnitPrice float64 `json:"unit_price" binding:"required,gte=0"`
	Notes     string  `json:"notes,omitempty"`
}

// RFQListFilter represents filters for listing RFQs
type RFQListFilter struct {
	Search   string    `form:"search"`
	Status   RFQStatus `form:"status"`
	DateFrom string    `form:"date_from"`
	DateTo   string    `form:"date_to"`
}

// RFQResponse represents the API response for an RFQ
type RFQAPIResponse struct {
	ID              uuid.UUID     `json:"id"`
	RFQNumber       string        `json:"rfq_number"`
	Title           string        `json:"title"`
	Description     *string       `json:"description,omitempty"`
	Status          RFQStatus     `json:"status"`
	Deadline        *time.Time    `json:"deadline,omitempty"`
	Terms           *string       `json:"terms,omitempty"`
	Notes           *string       `json:"notes,omitempty"`
	WinnerID        *uuid.UUID    `json:"winner_id,omitempty"`
	WinnerName      string        `json:"winner_name,omitempty"`
	Items           []RFQItem     `json:"items,omitempty"`
	Invitations     []RFQInvite   `json:"invitations,omitempty"`
	Responses       []RFQResponse `json:"responses,omitempty"`
	InvitationCount int           `json:"invitation_count"`
	ResponseCount   int           `json:"response_count"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
}
