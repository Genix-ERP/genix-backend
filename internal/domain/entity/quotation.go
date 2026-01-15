package entity

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// QuotationStatus represents the status of a quotation
type QuotationStatus string

const (
	QuotationStatusDraft     QuotationStatus = "draft"
	QuotationStatusSent      QuotationStatus = "sent"
	QuotationStatusViewed    QuotationStatus = "viewed"
	QuotationStatusAccepted  QuotationStatus = "accepted"
	QuotationStatusRejected  QuotationStatus = "rejected"
	QuotationStatusExpired   QuotationStatus = "expired"
	QuotationStatusConverted QuotationStatus = "converted"
)

// Quotation represents a sales quotation/proposal
type Quotation struct {
	ID                  uuid.UUID       `json:"id" db:"id"`
	TenantID            uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	QuotationNumber     string          `json:"quotation_number" db:"quotation_number"`
	OpportunityID       *uuid.UUID      `json:"opportunity_id,omitempty" db:"opportunity_id"`
	ContactID           *uuid.UUID      `json:"contact_id,omitempty" db:"contact_id"`
	QuotationDate       time.Time       `json:"quotation_date" db:"quotation_date"`
	ExpiryDate          *time.Time      `json:"expiry_date,omitempty" db:"expiry_date"`
	Status              QuotationStatus `json:"status" db:"status"`
	Subtotal            float64         `json:"subtotal" db:"subtotal"`
	DiscountAmount      float64         `json:"discount_amount" db:"discount_amount"`
	DiscountPercent     float64         `json:"discount_percent" db:"discount_percent"`
	TaxAmount           float64         `json:"tax_amount" db:"tax_amount"`
	TotalAmount         float64         `json:"total_amount" db:"total_amount"`
	Currency            string          `json:"currency" db:"currency"`
	Title               *string         `json:"title,omitempty" db:"title"`
	Introduction        *string         `json:"introduction,omitempty" db:"introduction"`
	TermsAndConditions  *string         `json:"terms_and_conditions,omitempty" db:"terms_and_conditions"`
	Notes               *string         `json:"notes,omitempty" db:"notes"`
	SentAt              *time.Time      `json:"sent_at,omitempty" db:"sent_at"`
	ViewedAt            *time.Time      `json:"viewed_at,omitempty" db:"viewed_at"`
	AcceptedAt          *time.Time      `json:"accepted_at,omitempty" db:"accepted_at"`
	RejectedAt          *time.Time      `json:"rejected_at,omitempty" db:"rejected_at"`
	RejectionReason     *string         `json:"rejection_reason,omitempty" db:"rejection_reason"`
	ConvertedToOrder    bool            `json:"converted_to_order" db:"converted_to_order"`
	OrderID             *uuid.UUID      `json:"order_id,omitempty" db:"order_id"`
	AssignedTo          *uuid.UUID      `json:"assigned_to,omitempty" db:"assigned_to"`
	CreatedBy           *uuid.UUID      `json:"created_by,omitempty" db:"created_by"`
	CreatedAt           time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at" db:"updated_at"`
	DeletedAt           sql.NullTime    `json:"-" db:"deleted_at"`

	// Relationships
	Lines []QuotationLine `json:"lines,omitempty"`

	// Populated from joins
	OpportunityName *string `json:"opportunity_name,omitempty" db:"opportunity_name"`
	ContactName     *string `json:"contact_name,omitempty" db:"contact_name"`
	AssignedToName  *string `json:"assigned_to_name,omitempty" db:"assigned_to_name"`
}

// QuotationLine represents a line item in a quotation
type QuotationLine struct {
	ID              uuid.UUID  `json:"id" db:"id"`
	QuotationID     uuid.UUID  `json:"quotation_id" db:"quotation_id"`
	LineNumber      int        `json:"line_number" db:"line_number"`
	ProductID       *uuid.UUID `json:"product_id,omitempty" db:"product_id"`
	Description     string     `json:"description" db:"description"`
	Quantity        float64    `json:"quantity" db:"quantity"`
	UnitPrice       float64    `json:"unit_price" db:"unit_price"`
	DiscountPercent float64    `json:"discount_percent" db:"discount_percent"`
	DiscountAmount  float64    `json:"discount_amount" db:"discount_amount"`
	TaxPercent      float64    `json:"tax_percent" db:"tax_percent"`
	TaxAmount       float64    `json:"tax_amount" db:"tax_amount"`
	LineTotal       float64    `json:"line_total" db:"line_total"`
	Notes           *string    `json:"notes,omitempty" db:"notes"`
	CreatedAt       time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at" db:"updated_at"`

	// Populated from joins
	ProductName *string `json:"product_name,omitempty" db:"product_name"`
	ProductSKU  *string `json:"product_sku,omitempty" db:"product_sku"`
}

// CreateQuotationInput represents input for creating a quotation
type CreateQuotationInput struct {
	OpportunityID      string               `json:"opportunity_id,omitempty"`
	ContactID          string               `json:"contact_id,omitempty"`
	QuotationDate      string               `json:"quotation_date" binding:"required"`
	ExpiryDate         string               `json:"expiry_date,omitempty"`
	Currency           string               `json:"currency,omitempty"`
	Title              string               `json:"title,omitempty"`
	Introduction       string               `json:"introduction,omitempty"`
	TermsAndConditions string               `json:"terms_and_conditions,omitempty"`
	Notes              string               `json:"notes,omitempty"`
	DiscountPercent    float64              `json:"discount_percent,omitempty"`
	AssignedTo         string               `json:"assigned_to,omitempty"`
	Lines              []CreateQuotationLineInput `json:"lines,omitempty"`
}

// CreateQuotationLineInput represents input for creating a quotation line
type CreateQuotationLineInput struct {
	ProductID       string  `json:"product_id,omitempty"`
	Description     string  `json:"description" binding:"required"`
	Quantity        float64 `json:"quantity" binding:"required,gt=0"`
	UnitPrice       float64 `json:"unit_price" binding:"required,gte=0"`
	DiscountPercent float64 `json:"discount_percent,omitempty"`
	TaxPercent      float64 `json:"tax_percent,omitempty"`
	Notes           string  `json:"notes,omitempty"`
}

// UpdateQuotationInput represents input for updating a quotation
type UpdateQuotationInput struct {
	OpportunityID      *string `json:"opportunity_id,omitempty"`
	ContactID          *string `json:"contact_id,omitempty"`
	QuotationDate      *string `json:"quotation_date,omitempty"`
	ExpiryDate         *string `json:"expiry_date,omitempty"`
	Status             *string `json:"status,omitempty"`
	Currency           *string `json:"currency,omitempty"`
	Title              *string `json:"title,omitempty"`
	Introduction       *string `json:"introduction,omitempty"`
	TermsAndConditions *string `json:"terms_and_conditions,omitempty"`
	Notes              *string `json:"notes,omitempty"`
	DiscountPercent    *float64 `json:"discount_percent,omitempty"`
	AssignedTo         *string `json:"assigned_to,omitempty"`
	Lines              []CreateQuotationLineInput `json:"lines,omitempty"`
}

// QuotationListFilter represents filters for listing quotations
type QuotationListFilter struct {
	Search        string `form:"search"`
	Status        string `form:"status"`
	OpportunityID string `form:"opportunity_id"`
	ContactID     string `form:"contact_id"`
	AssignedTo    string `form:"assigned_to"`
	DateFrom      string `form:"date_from"`
	DateTo        string `form:"date_to"`
	MinAmount     *float64 `form:"min_amount"`
	MaxAmount     *float64 `form:"max_amount"`
}

// QuotationResponse represents the API response for a quotation
type QuotationResponse struct {
	ID                  uuid.UUID       `json:"id"`
	QuotationNumber     string          `json:"quotation_number"`
	OpportunityID       *uuid.UUID      `json:"opportunity_id,omitempty"`
	OpportunityName     *string         `json:"opportunity_name,omitempty"`
	ContactID           *uuid.UUID      `json:"contact_id,omitempty"`
	ContactName         *string         `json:"contact_name,omitempty"`
	QuotationDate       time.Time       `json:"quotation_date"`
	ExpiryDate          *time.Time      `json:"expiry_date,omitempty"`
	Status              QuotationStatus `json:"status"`
	Subtotal            float64         `json:"subtotal"`
	DiscountAmount      float64         `json:"discount_amount"`
	DiscountPercent     float64         `json:"discount_percent"`
	TaxAmount           float64         `json:"tax_amount"`
	TotalAmount         float64         `json:"total_amount"`
	Currency            string          `json:"currency"`
	Title               *string         `json:"title,omitempty"`
	Introduction        *string         `json:"introduction,omitempty"`
	TermsAndConditions  *string         `json:"terms_and_conditions,omitempty"`
	Notes               *string         `json:"notes,omitempty"`
	SentAt              *time.Time      `json:"sent_at,omitempty"`
	ViewedAt            *time.Time      `json:"viewed_at,omitempty"`
	AcceptedAt          *time.Time      `json:"accepted_at,omitempty"`
	RejectedAt          *time.Time      `json:"rejected_at,omitempty"`
	ConvertedToOrder    bool            `json:"converted_to_order"`
	AssignedTo          *uuid.UUID      `json:"assigned_to,omitempty"`
	AssignedToName      *string         `json:"assigned_to_name,omitempty"`
	Lines               []QuotationLine `json:"lines,omitempty"`
	LineCount           int             `json:"line_count"`
	IsExpired           bool            `json:"is_expired"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

// QuotationStats represents quotation statistics
type QuotationStats struct {
	TotalQuotations    int            `json:"total_quotations"`
	DraftCount         int            `json:"draft_count"`
	SentCount          int            `json:"sent_count"`
	AcceptedCount      int            `json:"accepted_count"`
	RejectedCount      int            `json:"rejected_count"`
	ExpiredCount       int            `json:"expired_count"`
	TotalValue         float64        `json:"total_value"`
	AcceptedValue      float64        `json:"accepted_value"`
	PendingValue       float64        `json:"pending_value"`
	AcceptanceRate     float64        `json:"acceptance_rate"`
	AverageValue       float64        `json:"average_value"`
	AverageTimeToClose int            `json:"average_time_to_close_days"`
	ByStatus           map[string]int `json:"by_status"`
}

// SendQuotationInput represents input for sending a quotation
type SendQuotationInput struct {
	EmailTo      []string `json:"email_to" binding:"required,min=1"`
	EmailCC      []string `json:"email_cc,omitempty"`
	EmailSubject string   `json:"email_subject,omitempty"`
	EmailBody    string   `json:"email_body,omitempty"`
	AttachPDF    bool     `json:"attach_pdf"`
}

// AcceptQuotationInput represents input for accepting a quotation
type AcceptQuotationInput struct {
	AcceptedBy    string `json:"accepted_by,omitempty"`
	SignatureData string `json:"signature_data,omitempty"`
	Notes         string `json:"notes,omitempty"`
}

// RejectQuotationInput represents input for rejecting a quotation
type RejectQuotationInput struct {
	Reason string `json:"reason,omitempty"`
	Notes  string `json:"notes,omitempty"`
}
